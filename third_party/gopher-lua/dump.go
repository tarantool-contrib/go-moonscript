package lua

// go-moonscript patch: string.dump and binary chunks. gopher-lua has no
// PUC Lua bytecode, so string.dump writes the function prototype of the
// copy (its instructions, constants, nested prototypes and debug
// information) in a format of its own, which Load (and so loadstring, load,
// loadfile and dofile) accepts back. The header starts with Lua 5.1's
// signature "\x1bLua", so that a chunk is taken for a binary one, followed
// by a version byte that is not PUC Lua's (0x51): PUC Lua rejects our
// chunks with "bad header in precompiled chunk", and we reject PUC Lua's
// (and LuaJIT's, "\x1bLJ") the same way. As in Lua 5.1, a loaded function
// has fresh upvalues (nil), detached from the dumped closure's, and the
// globals of the loading function as its environment. Loading checks the
// operands that index the prototype's own tables (constants, upvalues,
// prototypes, jump targets) as luaG_checkcode does; registers are not
// checked (gopher-lua's NumUsedRegisters is no exact bound): an access out
// of the registry is a Go panic, which PCall turns into an error.

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strings"
)

const (
	dumpSignature   = "\x1bLua"
	dumpSignatureLJ = "\x1bLJ" // under DialectLuaJIT
	dumpVersion     = 0x5f     // not 0x51, PUC Lua 5.1's, nor LuaJIT's 1 or 2
	dumpFormat      = 0
	dumpMagic       = "gopher-lua" // and the version of the format
	dumpRevision    = 1
)

// dumpHeader is the start of every dump, dumpHeaderLJ of those made under
// DialectLuaJIT (both are loaded in either dialect).
var (
	dumpHeader   = dumpSignature + string([]byte{dumpVersion, dumpFormat}) + dumpMagic + string([]byte{dumpRevision})
	dumpHeaderLJ = dumpSignatureLJ + string([]byte{dumpVersion, dumpFormat}) + dumpMagic + string([]byte{dumpRevision})
)

// IsDump reports whether a chunk is a dump of this VM (string.dump in
// either dialect).
func IsDump(chunk string) bool {
	return strings.HasPrefix(chunk, dumpHeader) || strings.HasPrefix(chunk, dumpHeaderLJ)
}

// IsBinaryChunk reports whether a chunk starts like a precompiled one,
// with Lua's signature character ("\x1b").
func IsBinaryChunk(chunk string) bool {
	return len(chunk) > 0 && chunk[0] == dumpSignature[0]
}

// DumpFunction is lua_dump: the binary chunk of a Lua function, or an
// error for a Go function.
func DumpFunction(fn *LFunction) ([]byte, error) {
	return dumpFunction(fn, dumpHeader, false)
}

// dumpFunction dumps fn after header; strip leaves out the names of
// locals, upvalues and calls (LuaJIT's string.dump(f, true)).
func dumpFunction(fn *LFunction, header string, strip bool) ([]byte, error) {
	if fn.IsG || fn.Proto == nil {
		return nil, errors.New("unable to dump given function")
	}
	d := &dumper{buf: []byte(header), strip: strip}
	d.proto(fn.Proto)
	return d.buf, nil
}

type dumper struct {
	buf   []byte
	strip bool
}

func (d *dumper) uint(n uint64) { d.buf = binary.AppendUvarint(d.buf, n) }
func (d *dumper) int(n int)     { d.buf = binary.AppendVarint(d.buf, int64(n)) }
func (d *dumper) str(s string) {
	d.uint(uint64(len(s)))
	d.buf = append(d.buf, s...)
}

const (
	dumpConstNil = iota
	dumpConstFalse
	dumpConstTrue
	dumpConstNumber
	dumpConstString
	dumpConstCdataNumeral // cdataNumeralFn (compile.go)
)

func (d *dumper) proto(fp *FunctionProto) {
	d.str(fp.SourceName)
	d.int(fp.LineDefined)
	d.int(fp.LastLineDefined)
	d.buf = append(d.buf, fp.NumUpvalues, fp.NumParameters, fp.IsVarArg, fp.NumUsedRegisters)
	d.uint(uint64(len(fp.Code)))
	for _, inst := range fp.Code {
		d.buf = binary.LittleEndian.AppendUint32(d.buf, inst)
	}
	d.uint(uint64(len(fp.Constants)))
	for _, k := range fp.Constants {
		switch v := k.(type) {
		case LNumber:
			d.buf = append(d.buf, dumpConstNumber)
			d.buf = binary.LittleEndian.AppendUint64(d.buf, math.Float64bits(float64(v)))
		case LString:
			d.buf = append(d.buf, dumpConstString)
			d.str(string(v))
		case LBool:
			if v {
				d.buf = append(d.buf, dumpConstTrue)
			} else {
				d.buf = append(d.buf, dumpConstFalse)
			}
		case *LFunction:
			if v == cdataNumeralFn {
				d.buf = append(d.buf, dumpConstCdataNumeral)
			} else {
				d.buf = append(d.buf, dumpConstNil)
			}
		default:
			d.buf = append(d.buf, dumpConstNil)
		}
	}
	d.uint(uint64(len(fp.FunctionPrototypes)))
	for _, p := range fp.FunctionPrototypes {
		d.proto(p)
	}
	d.uint(uint64(len(fp.DbgSourcePositions)))
	for _, line := range fp.DbgSourcePositions {
		d.int(line)
	}
	if d.strip {
		d.uint(0)
		d.uint(0)
		d.uint(0)
		return
	}
	d.uint(uint64(len(fp.DbgLocals)))
	for _, l := range fp.DbgLocals {
		d.str(l.Name)
		d.int(l.StartPc)
		d.int(l.EndPc)
	}
	d.uint(uint64(len(fp.DbgCalls)))
	for _, c := range fp.DbgCalls {
		d.str(c.Name)
		d.int(c.Pc)
	}
	d.uint(uint64(len(fp.DbgUpvalues)))
	for _, name := range fp.DbgUpvalues {
		d.str(name)
	}
}

type undumper struct {
	data []byte
	err  string
}

func (u *undumper) fail(why string) {
	if u.err == "" {
		u.err = why
	}
	u.data = nil
}

func (u *undumper) uint() uint64 {
	n, k := binary.Uvarint(u.data)
	if k <= 0 {
		u.fail("unexpected end")
		return 0
	}
	u.data = u.data[k:]
	return n
}

func (u *undumper) int() int {
	n, k := binary.Varint(u.data)
	if k <= 0 || n < math.MinInt32 || n > math.MaxInt32 {
		u.fail("unexpected end")
		return 0
	}
	u.data = u.data[k:]
	return int(n)
}

// count reads a number of items of at least size bytes each.
func (u *undumper) count(size int) int {
	n := u.uint()
	if n > uint64(len(u.data)/size) {
		u.fail("unexpected end")
		return 0
	}
	return int(n)
}

func (u *undumper) bytes(n int) []byte {
	if n > len(u.data) {
		u.fail("unexpected end")
		return nil
	}
	b := u.data[:n]
	u.data = u.data[n:]
	return b
}

func (u *undumper) str() string {
	return string(u.bytes(u.count(1)))
}

func (u *undumper) byte() byte {
	if b := u.bytes(1); b != nil {
		return b[0]
	}
	return 0
}

func (u *undumper) proto(depth int) *FunctionProto {
	if depth > 200 { // LUAI_MAXCCALLS
		u.fail("bad code")
		return nil
	}
	fp := &FunctionProto{}
	fp.SourceName = u.str()
	fp.LineDefined = u.int()
	fp.LastLineDefined = u.int()
	fp.NumUpvalues = u.byte()
	fp.NumParameters = u.byte()
	fp.IsVarArg = u.byte()
	fp.NumUsedRegisters = u.byte()
	fp.Code = make([]uint32, u.count(4))
	for i := range fp.Code {
		if b := u.bytes(4); b != nil {
			fp.Code[i] = binary.LittleEndian.Uint32(b)
		}
	}
	fp.Constants = make([]LValue, u.count(1))
	for i := range fp.Constants {
		switch u.byte() {
		case dumpConstNil:
			fp.Constants[i] = LNil
		case dumpConstFalse:
			fp.Constants[i] = LFalse
		case dumpConstTrue:
			fp.Constants[i] = LTrue
		case dumpConstNumber:
			if b := u.bytes(8); b != nil {
				fp.Constants[i] = LNumber(math.Float64frombits(binary.LittleEndian.Uint64(b)))
			}
		case dumpConstString:
			fp.Constants[i] = LString(u.str())
		case dumpConstCdataNumeral:
			fp.Constants[i] = cdataNumeralFn
		default:
			u.fail("bad constant")
		}
	}
	fp.FunctionPrototypes = make([]*FunctionProto, u.count(1))
	for i := range fp.FunctionPrototypes {
		fp.FunctionPrototypes[i] = u.proto(depth + 1)
	}
	fp.DbgSourcePositions = make([]int, u.count(1))
	for i := range fp.DbgSourcePositions {
		fp.DbgSourcePositions[i] = u.int()
	}
	fp.DbgLocals = make([]*DbgLocalInfo, u.count(3))
	for i := range fp.DbgLocals {
		fp.DbgLocals[i] = &DbgLocalInfo{Name: u.str(), StartPc: u.int(), EndPc: u.int()}
	}
	fp.DbgCalls = make([]DbgCall, u.count(2))
	for i := range fp.DbgCalls {
		fp.DbgCalls[i] = DbgCall{Name: u.str(), Pc: u.int()}
	}
	fp.DbgUpvalues = make([]string, u.count(1))
	for i := range fp.DbgUpvalues {
		fp.DbgUpvalues[i] = u.str()
	}
	if u.err != "" {
		return nil
	}
	if !checkProto(fp) {
		u.fail("bad code")
		return nil
	}
	fp.stringConstants = make([]string, len(fp.Constants))
	fp.stringConstHashes = make([]uint32, len(fp.Constants))
	for i, k := range fp.Constants {
		if s, ok := k.(LString); ok {
			fp.stringConstants[i] = string(s)
		}
		fp.stringConstHashes[i] = hashString(fp.stringConstants[i])
	}
	return fp
}

// checkProto is luaG_checkcode for the instructions of the copy: every
// operand that indexes a table of the prototype (a constant, an upvalue, a
// prototype, a jump target) is in range, table sizes are Lua 5.1's at
// most, the instructions that follow an instruction
// (MOVEN, CLOSURE, LOADBOOL and tests skipping one) exist, and the code
// ends with a RETURN.
func checkProto(fp *FunctionProto) bool {
	n := len(fp.Code)
	if n == 0 || opGetOpCode(fp.Code[n-1]) != OP_RETURN {
		return false
	}
	if len(fp.DbgSourcePositions) != n || len(fp.DbgUpvalues) > int(fp.NumUpvalues) ||
		int(fp.NumParameters) > int(fp.NumUsedRegisters) {
		return false
	}
	nk := len(fp.Constants)
	for _, p := range fp.FunctionPrototypes {
		if p == nil {
			return false
		}
	}
	rk := func(v int) bool { return !opIsK(v) || v&^opBitRk < nk }
	jump := func(pc, sbx int) bool { t := pc + 1 + sbx; return t >= 0 && t < n }
	for pc := 0; pc < n; pc++ {
		inst := fp.Code[pc]
		op := opGetOpCode(inst)
		if op > opCodeMax {
			return false
		}
		b, c, bx, sbx := opGetArgB(inst), opGetArgC(inst), opGetArgBx(inst), opGetArgSbx(inst)
		prop := opProps[op]
		if prop.Type == opTypeABC {
			if prop.ModeArgB == opArgModeK && !rk(b) || prop.ModeArgC == opArgModeK && !rk(c) {
				return false
			}
		}
		switch op {
		case OP_LOADK, OP_GETGLOBAL, OP_SETGLOBAL:
			if bx >= nk {
				return false
			}
		case OP_GETTABLEKS, OP_SETTABLEKS, OP_SELF:
			if !rk(b) || !rk(c) {
				return false
			}
		case OP_NEWTABLE:
			if fb2Int(b) > 1<<26 || fb2Int(c) > 1<<26 { // MAXASIZE, MAXBITS of Lua 5.1
				return false
			}
		case OP_GETUPVAL, OP_SETUPVAL:
			if b >= int(fp.NumUpvalues) {
				return false
			}
		case OP_MOVEN:
			if pc+c >= n {
				return false
			}
			for i := 1; i <= c; i++ {
				if opGetOpCode(fp.Code[pc+i]) != OP_MOVE {
					return false
				}
			}
			pc += c
		case OP_JMP, OP_FORLOOP, OP_FORPREP:
			if !jump(pc, sbx) {
				return false
			}
		case OP_LOADBOOL, OP_EQ, OP_LT, OP_LE, OP_TEST, OP_TESTSET, OP_TFORLOOP:
			if pc+2 > n {
				return false
			}
		case OP_SETLIST:
			if c == 0 && pc+1 >= n {
				return false
			}
		case OP_CLOSURE:
			if bx >= len(fp.FunctionPrototypes) {
				return false
			}
			nup := int(fp.FunctionPrototypes[bx].NumUpvalues)
			if pc+nup >= n {
				return false
			}
			for i := 1; i <= nup; i++ {
				next := fp.Code[pc+i]
				switch opGetOpCode(next) {
				case OP_MOVE:
				case OP_GETUPVAL:
					if opGetArgB(next) >= int(fp.NumUpvalues) {
						return false
					}
				default:
					return false
				}
			}
			pc += nup
		}
	}
	return true
}

// undumpChunk is luaU_undump: the function of a binary chunk, or why it
// cannot be loaded ("bad header", "unexpected end", ...) and whether it is
// in the header.
func undumpChunk(data []byte) (fp *FunctionProto, why string, header bool) {
	const pucHeaderSize = 12 // what luaU_undump reads before checking it
	dumpHeader := dumpHeader
	if strings.HasPrefix(string(data), dumpSignatureLJ) {
		dumpHeader = dumpHeaderLJ
	}
	n := min(len(data), len(dumpHeader))
	switch {
	case len(data) < pucHeaderSize:
		return nil, "unexpected end", true
	case string(data[:n]) != dumpHeader[:n]:
		return nil, "bad header", true
	case n < len(dumpHeader):
		return nil, "unexpected end", true
	}
	u := &undumper{data: data[len(dumpHeader):]}
	fp = u.proto(0)
	if u.err == "" && len(u.data) != 0 {
		u.fail("bad code") // trailing bytes
	}
	if u.err != "" {
		return nil, u.err, false
	}
	return fp, "", false
}

// newUndumpedFunction is f_parser's closure for a loaded prototype: fresh
// upvalues, all nil.
func newUndumpedFunction(fp *FunctionProto, env *LTable) *LFunction {
	fn := newLFunctionL(fp, env, int(fp.NumUpvalues))
	for i := range fn.Upvalues {
		fn.Upvalues[i] = &Upvalue{value: LNil, closed: true}
	}
	return fn
}

// undumpError is the error of a binary chunk named name that cannot be
// loaded: luaU_undump's "<name>: <why> in precompiled chunk", where name
// is the chunk name without its '@' or '=', "binary string" for a chunk
// named by its own text (gopher-lua's display names: `[string "..."]` of a
// text starting with the signature); LuaJIT's "<name>: cannot load
// incompatible bytecode" (or "malformed" when the header is right), name
// "(binary)" for a chunk named by its text.
func undumpError(ls *LState, name, why string, header bool) error {
	binName := "binary string"
	if ls.Dialect() == DialectLuaJIT {
		binName = "(binary)"
		if header {
			why = "cannot load incompatible bytecode"
		} else {
			why = "cannot load malformed bytecode"
		}
	} else {
		why += " in precompiled chunk"
	}
	switch {
	case len(name) > 0 && (name[0] == '@' || name[0] == '='):
		name = name[1:]
	case IsBinaryChunk(name), strings.HasPrefix(name, "[string \"") && IsBinaryChunk(name[9:]):
		name = binName
	}
	return newApiErrorS(ApiErrorSyntax, name+": "+why)
}

// loadBinary loads the chunk of reader if it is a binary one; otherwise it
// returns a reader of the same text and no function.
func (ls *LState) loadBinary(reader io.Reader, name string) (io.Reader, *LFunction, error) {
	br, ok := reader.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(reader)
	}
	if b, err := br.Peek(1); err != nil || !IsBinaryChunk(string(b)) {
		return br, nil, nil
	}
	data, err := io.ReadAll(br)
	if err != nil {
		return br, nil, newApiErrorE(ApiErrorFile, err)
	}
	fp, why, header := undumpChunk(data)
	if fp == nil {
		return br, nil, undumpError(ls, name, why, header)
	}
	return br, newUndumpedFunction(fp, ls.currentEnv()), nil
}
