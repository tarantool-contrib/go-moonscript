package lua

// go-moonscript patch: the library side of DialectLuaJIT, LuaJIT 2.1 as the
// reference luajit is (2.1.0-beta3, built without LUAJIT_ENABLE_LUA52COMPAT,
// with the JIT compiler and the FFI, on x64 Linux), minus what an
// interpreter written in Go cannot have:
//
//   - the bit library (lib_bit.c: 32-bit operations on doubles) and a jit
//     module for an interpreter-only LuaJIT (see OpenJit);
//   - base: load(chunk [, chunkname [, mode [, env]]]) with a string chunk,
//     loadstring doing the same, loadfile(filename [, mode [, env]]),
//     xpcall(f, handler, ...) passing the arguments to f, assert raising any
//     message value, tonumber(s, base) negating after a '-' (in
//     baselib.go), collectgarbage("isrunning");
//   - table.move, table.new and table.clear (require "table.new" / "table.clear");
//   - math.log(x [, base]), math.max/min as LuaJIT's maxsd/minsd, no math.mod;
//   - string.rep(s, n [, sep]), string.format as lj_strfmt
//     (luajit_strformat.go), numbers converted to strings as LuaJIT does, no
//     string.gfind;
//   - coroutine.isyieldable, package.searchpath, debug.upvalueid,
//     debug.upvaluejoin, debug.getinfo's "u" fields nparams and isvararg,
//     debug.getlocal(f, n) for the parameters of a function;
//   - os.exit(true/false), os.date passing the E and O modifiers.
//
// Not provided: the ffi, string.buffer (not in 2.1.0-beta3), jit.opt,
// jit.util, jit.profile and the rest of the JIT compiler's interfaces.
//
// The libraries call openLuaJIT at the end of their Open function; under
// DialectLua51 it does nothing and the libraries are Lua 5.1's.

import (
	"math"
	"math/bits"
	"os"
	goruntime "runtime"
	"strings"
)

const (
	// BitLibName is the name of the LuaJIT bit library (DialectLuaJIT).
	BitLibName = "bit"
	// JitLibName is the name of the LuaJIT jit module (DialectLuaJIT).
	JitLibName = "jit"
)

// LuaJITVersion and LuaJITVersionNum are jit.version and jit.version_num:
// those of the LuaJIT the dialect follows.
const (
	LuaJITVersion    = "LuaJIT 2.1.0-beta3"
	LuaJITVersionNum = 20100
)

// openLuaJIT adapts the library lib (the table mod it has just opened) to
// the LuaJIT dialect; it does nothing for another dialect.
func openLuaJIT(L *LState, lib string, mod LValue) {
	if L.Dialect() != DialectLuaJIT {
		return
	}
	tb, ok := mod.(*LTable)
	if !ok {
		return
	}
	switch lib {
	case BaseLibName:
		if gc, ok := tb.RawGetString("collectgarbage").(*LFunction); ok && gc.IsG {
			tb.RawSetString("collectgarbage", L.NewFunction(jitCollectGarbage(gc.GFunction)))
		}
	case TabLibName:
		tb.RawSetString("move", L.NewFunction(jitTableMove))
		tb.RawSetString("setn", LNil)
		if preload, ok := L.GetField(L.GetField(L.Get(GlobalsIndex), "package"), "preload").(*LTable); ok {
			preload.RawSetString("table.new", L.NewFunction(func(L *LState) int {
				L.Push(L.NewFunction(jitTableNew))
				return 1
			}))
			preload.RawSetString("table.clear", L.NewFunction(func(L *LState) int {
				L.Push(L.NewFunction(jitTableClear))
				return 1
			}))
		}
	case MathLibName:
		tb.RawSetString("log", L.NewFunction(jitMathLog))
		tb.RawSetString("max", L.NewFunction(jitMathMinMax(false)))
		tb.RawSetString("min", L.NewFunction(jitMathMinMax(true)))
		tb.RawSetString("mod", LNil)
	case StringLibName:
		tb.RawSetString("rep", L.NewFunction(jitStrRep))
		tb.RawSetString("gfind", LNil)
	case CoroutineLibName:
		tb.RawSetString("isyieldable", L.NewFunction(jitCoIsYieldable))
	case LoadLibName:
		tb.RawSetString("searchpath", L.NewFunction(jitSearchPath))
	case DebugLibName:
		tb.RawSetString("upvalueid", L.NewFunction(jitUpvalueID))
		tb.RawSetString("upvaluejoin", L.NewFunction(jitUpvalueJoin))
		if f, ok := tb.RawGetString("getinfo").(*LFunction); ok && f.IsG {
			tb.RawSetString("getinfo", L.NewFunction(jitGetInfo(f.GFunction)))
		}
		if f, ok := tb.RawGetString("getlocal").(*LFunction); ok && f.IsG {
			tb.RawSetString("getlocal", L.NewFunction(jitGetLocal(f.GFunction)))
		}
	}
}

// jitCheckInt is lj_lib_checkint: a number (or a string convertible to
// one) converted to an int32 like a C cast on x86-64 (truncated; INT32_MIN
// out of range or for a NaN).
func jitCheckInt(L *LState, n int) int32 {
	return jitNum2Int(float64(L.CheckNumber(n)))
}

func jitNum2Int(f float64) int32 {
	if f > -2147483649 && f < 2147483648 {
		return int32(f)
	}
	return math.MinInt32
}

// jitOptString is lj_lib_optstr: nil or absent gives ok == false.
func jitOptString(L *LState, n int) (string, bool) {
	if L.Get(n) == LNil {
		return "", false
	}
	return L.CheckString(n), true
}

/* base {{{ */

// jitLoad is LuaJIT 2.1's load (and loadstring, which does the same):
// load(chunk [, chunkname [, mode [, env]]]) where the chunk is a string
// (or a number) or a reader function; a mode without 't' rejects source
// chunks; a table env becomes the environment of the loaded function.
func jitLoad(L *LState) int {
	name, hasName := jitOptString(L, 2)
	mode, hasMode := jitOptString(L, 3)
	env, _ := L.Get(4).(*LTable)
	// go-moonscript patch: Lua chunk names, the string itself or "=(load)"
	// by default, as in LuaJIT (chunkname.go)
	var code string
	if lv := L.Get(1); lv.Type() == LTString || lv.Type() == LTNumber {
		code = L.CheckString(1)
		if !hasName {
			name = code
		}
	} else {
		fn := L.CheckFunction(1)
		if !hasName {
			name = "=(load)"
		}
		var buf strings.Builder
		top := L.GetTop()
		for {
			L.SetTop(top)
			L.Push(fn)
			if err := L.PCall(0, 1, nil); err != nil {
				L.SetTop(top)
				L.Push(LNil)
				if ae, ok := err.(*ApiError); ok {
					L.Push(ae.Object)
				} else {
					L.Push(LString(err.Error()))
				}
				return 2
			}
			ret := L.Get(-1)
			if ret == LNil {
				break
			}
			if !LVCanConvToString(ret) {
				L.SetTop(top)
				L.Push(LNil)
				L.Push(LString(L.whereMessage("reader function must return a string")))
				return 2
			}
			s := L.lvAsString(ret)
			if s == "" {
				break
			}
			buf.WriteString(s)
		}
		L.SetTop(top)
		code = buf.String()
	}
	return jitLoadChecked(L, code, name, mode, hasMode, env)
}

// jitLoadChecked loads code as LuaJIT's load and loadfile do once they
// have read it: the mode and precompiled chunks checked, the environment
// set.
func jitLoadChecked(L *LState, code, chunkname, mode string, hasMode bool, env *LTable) int {
	if msg := JITLoadModeError(code, mode, hasMode); msg != "" {
		L.Push(LNil)
		L.Push(LString(msg))
		return 2
	}
	if JITIsBinaryChunk(code) && !IsDump(code) {
		// bytecode of another VM; this VM's own dumps load (dump.go)
		L.Push(LNil)
		L.Push(LString(JITBytecodeError(chunkname)))
		return 2
	}
	n := loadaux(L, strings.NewReader(code), chunkname)
	if fn, ok := L.Get(-1).(*LFunction); ok && n == 1 && env != nil {
		fn.Env = env
	}
	return n
}

// JITIsBinaryChunk reports whether a chunk is precompiled (starts with
// LUA_SIGNATURE's escape): LuaJIT's load and loadfile tell the two kinds
// apart by it.
func JITIsBinaryChunk(code string) bool { return strings.HasPrefix(code, "\x1b") }

// JITBytecodeError is LuaJIT's error for a precompiled chunk, which this VM
// cannot load (as bytecode of another VM): the chunk name shortened as
// lj_bcread does ("(binary)" for a name that is the chunk itself).
func JITBytecodeError(chunkname string) string {
	switch {
	case JITIsBinaryChunk(chunkname):
		chunkname = "(binary)"
	case strings.HasPrefix(chunkname, "=") || strings.HasPrefix(chunkname, "@"):
		chunkname = chunkname[1:]
	}
	return chunkname + ": cannot load incompatible bytecode"
}

// JITLoadModeError is LuaJIT's check of the mode argument of load and
// loadfile: the message for a chunk the mode does not allow, "" if it is
// allowed (hasMode false: no mode given, anything goes). Bytecode after a
// "#" line or a UTF-8 BOM is refused before the mode is looked at
// (lj_lex_setup), whatever the mode; the parser refuses it too.
func JITLoadModeError(code, mode string, hasMode bool) string {
	if hasMode && jitHeaderBytecode(code) {
		return "cannot load malformed bytecode"
	}
	kind := byte('t')
	if JITIsBinaryChunk(code) {
		kind = 'b'
	}
	if hasMode && strings.IndexByte(mode, kind) < 0 {
		return "attempt to load chunk with wrong mode"
	}
	return ""
}

// jitHeaderBytecode reports whether code starts with a UTF-8 BOM or a "#"
// line, or both, and bytecode after them, as lj_lex_setup reads them.
func jitHeaderBytecode(code string) bool {
	rest := strings.TrimPrefix(code, "\xef\xbb\xbf")
	if strings.HasPrefix(rest, "#") {
		i := strings.IndexAny(rest, "\n\r")
		if i < 0 {
			return false
		}
		rest = rest[i:]
		if len(rest) > 1 && rest[1] != rest[0] && (rest[1] == '\n' || rest[1] == '\r') {
			rest = rest[2:] // "\r\n" or "\n\r"
		} else {
			rest = rest[1:]
		}
	}
	return len(rest) < len(code) && JITIsBinaryChunk(rest)
}

// jitLoadFile is LuaJIT 2.1's loadfile(filename [, mode [, env]]).
func jitLoadFile(L *LState) int {
	fname, hasName := jitOptString(L, 1)
	mode, hasMode := jitOptString(L, 2)
	env, _ := L.Get(3).(*LTable)
	// go-moonscript patch: luaL_loadfilex's chunk name ("@" + the file name,
	// "=stdin") and messages; the parser skips a first "#" line
	// (lj_lex_setup; luaL_loadfilex reads the file as it is), and refuses
	// bytecode after it (chunkname.go)
	code, chunkname, err := L.readChunkFile(fname, !hasName)
	if err != nil {
		return pushLoadResult(L, nil, err)
	}
	return jitLoadChecked(L, code, chunkname, mode, hasMode, env)
}

// jitAssert is LuaJIT 2.1's assert: a string or number message is raised
// with the caller's position, any other message value as it is.
func jitAssert(L *LState) int {
	L.CheckAny(1)
	if L.ToBool(1) {
		return L.GetTop()
	}
	switch msg := L.Get(2); {
	case L.GetTop() < 2:
		L.RaiseError("assertion failed!")
	case msg.Type() == LTString || msg.Type() == LTNumber:
		L.RaiseError("%s", L.lvAsString(msg))
	default:
		L.Error(msg, 0)
	}
	return 0
}

// jitCollectGarbage adds LuaJIT 2.1's "isrunning" option to collectgarbage
// (whether "stop" stopped the collector since the last "restart").
func jitCollectGarbage(gc LGFunction) LGFunction {
	running := true
	return func(L *LState) int {
		switch L.OptString(1, "collect") {
		case "isrunning":
			L.Push(LBool(running))
			return 1
		case "stop":
			running = false
		case "restart":
			running = true
		}
		return gc(L)
	}
}

/* }}} */

/* bit {{{ */

// OpenBit opens LuaJIT's bit library (BitLibName): operations on 32-bit
// integers, where every argument is converted as lj_num2bit does (rounded
// to the nearest integer, ties to even, modulo 2^32) and every result is
// a signed 32-bit integer.
func OpenBit(L *LState) int {
	mod := L.RegisterModule(BitLibName, bitFuncs)
	L.Push(mod)
	return 1
}

var bitFuncs = map[string]LGFunction{
	"tobit":   bitToBit,
	"tohex":   bitToHex,
	"bnot":    bitBNot,
	"band":    bitBand,
	"bor":     bitBor,
	"bxor":    bitBxor,
	"lshift":  bitLShift,
	"rshift":  bitRShift,
	"arshift": bitARShift,
	"rol":     bitRol,
	"ror":     bitRor,
	"bswap":   bitBSwap,
}

// jitToBit is lj_num2bit: the low 32 bits of n + 2^52 + 2^51.
func jitToBit(n float64) int32 {
	return int32(uint32(math.Float64bits(n + 6755399441055744.0)))
}

func bitArg(L *LState, n int) int32 { return jitToBit(float64(L.CheckNumber(n))) }

func bitPush(L *LState, v int32) int {
	L.Push(LNumber(v))
	return 1
}

func bitToBit(L *LState) int { return bitPush(L, bitArg(L, 1)) }
func bitBNot(L *LState) int  { return bitPush(L, ^bitArg(L, 1)) }
func bitBSwap(L *LState) int {
	return bitPush(L, int32(bits.ReverseBytes32(uint32(bitArg(L, 1)))))
}

func bitFold(op func(a, b int32) int32) LGFunction {
	return func(L *LState) int {
		v := bitArg(L, 1)
		for i := 2; i <= L.GetTop(); i++ {
			v = op(v, bitArg(L, i))
		}
		return bitPush(L, v)
	}
}

var (
	bitBand = bitFold(func(a, b int32) int32 { return a & b })
	bitBor  = bitFold(func(a, b int32) int32 { return a | b })
	bitBxor = bitFold(func(a, b int32) int32 { return a ^ b })
)

func bitShift(op func(x uint32, n uint) uint32) LGFunction {
	return func(L *LState) int {
		x, n := bitArg(L, 1), bitArg(L, 2)
		return bitPush(L, int32(op(uint32(x), uint(n&31))))
	}
}

var (
	bitLShift  = bitShift(func(x uint32, n uint) uint32 { return x << n })
	bitRShift  = bitShift(func(x uint32, n uint) uint32 { return x >> n })
	bitARShift = bitShift(func(x uint32, n uint) uint32 { return uint32(int32(x) >> n) })
	bitRol     = bitShift(func(x uint32, n uint) uint32 { return bits.RotateLeft32(x, int(n)) })
	bitRor     = bitShift(func(x uint32, n uint) uint32 { return bits.RotateLeft32(x, -int(n)) })
)

// bitToHex is bit.tohex(x [, n]) of LuaJIT 2.1 with the FFI: x as an
// unsigned 32-bit value, n hex digits (8 by default; uppercase when n is
// negative), zero-padded, keeping the low n digits.
func bitToHex(L *LState) int {
	b := uint64(uint32(bitArg(L, 1)))
	n := int32(8)
	if L.GetTop() >= 2 {
		n = bitArg(L, 2)
	}
	digits := "0123456789abcdef"
	if n < 0 {
		n = -n // stays negative for INT32_MIN, as in C
		digits = "0123456789ABCDEF"
	}
	if n < 16 {
		b &= 1<<(uint32(4*n)&63) - 1
	}
	prec := int((uint32(n)+1)&255) - 1
	var buf []byte
	for k := b; k != 0; k >>= 4 {
		buf = append(buf, digits[k&15])
	}
	if b == 0 && prec != 0 {
		buf = append(buf, '0')
	}
	for len(buf) < prec {
		buf = append(buf, '0')
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	L.Push(LString(buf))
	return 1
}

/* }}} */

/* jit {{{ */

// OpenJit opens the jit module (JitLibName) of a LuaJIT without its JIT
// compiler (built with LUAJIT_DISABLE_JIT), which is what this VM is:
//
//   - version, version_num: LuaJIT 2.1.0-beta3's, the LuaJIT the dialect
//     follows (code tests version_num >= 20100 for 2.1's extensions);
//   - os, arch: jit.os and jit.arch of LuaJIT for the host ("Linux", "x64",
//     ...; GOARCH where LuaJIT has no port);
//   - status() returns false (no compiler, so no CPU or optimization flags
//     either); off() and flush() succeed and do nothing; on() raises "JIT
//     compiler permanently disabled by build option"; with a function
//     argument (or true for the caller) they set per-function flags only,
//     so they succeed for a Lua function and do nothing;
//   - attach(f [, event]) checks its arguments and registers nothing: the
//     VM raises no "bc", "trace", "record" or "texit" events;
//   - no jit.opt (it exists only with the compiler; code tests
//     jit.opt ~= nil for it), and no jit.util or jit.profile modules: they
//     expose LuaJIT's bytecode, traces and sampling profiler, which this VM
//     does not have.
func OpenJit(L *LState) int {
	mod := L.RegisterModule(JitLibName, map[string]LGFunction{
		"on":     jitOn,
		"off":    jitOff,
		"flush":  jitOff,
		"status": jitStatus,
		"attach": jitAttach,
	}).(*LTable)
	mod.RawSetString("version", LString(LuaJITVersion))
	mod.RawSetString("version_num", LNumber(LuaJITVersionNum))
	mod.RawSetString("os", LString(jitOS(goruntime.GOOS)))
	mod.RawSetString("arch", LString(jitArch(goruntime.GOARCH)))
	L.Push(mod)
	return 1
}

// jitOS is LJ_OS_NAME for a GOOS.
func jitOS(goos string) string {
	switch goos {
	case "linux", "android":
		return "Linux"
	case "darwin", "ios":
		return "OSX"
	case "windows":
		return "Windows"
	case "freebsd", "netbsd", "openbsd", "dragonfly":
		return "BSD"
	case "solaris", "illumos", "aix", "hurd":
		return "POSIX"
	}
	return "Other"
}

// jitArch is LJ_ARCH_NAME for a GOARCH, the GOARCH itself where LuaJIT 2.1
// has no port.
func jitArch(goarch string) string {
	switch goarch {
	case "386":
		return "x86"
	case "amd64":
		return "x64"
	case "arm":
		return "arm"
	case "arm64":
		return "arm64"
	case "arm64be":
		return "arm64be"
	case "ppc":
		return "ppc"
	case "mips":
		return "mips"
	case "mipsle":
		return "mipsel"
	case "mips64":
		return "mips64"
	case "mips64le":
		return "mips64el"
	}
	return goarch
}

// jitSetMode is lib_jit.c's setjitmode without the compiler: whether the
// arguments select the whole engine (none or nil), and an argument error
// for anything but a Lua function or true (the calling Lua function).
func jitSetMode(L *LState) (engine bool) {
	switch v := L.Get(1).(type) {
	case *LNilType:
		return true
	case *LFunction:
		if !v.IsG {
			return false
		}
	case LBool:
		if dbg, ok := L.GetStack(1); bool(v) && ok && dbg.frame != nil && !dbg.frame.Fn.IsG {
			return false
		}
	}
	L.TypeError(1, LTFunction)
	return false
}

func jitOn(L *LState) int {
	if jitSetMode(L) {
		L.RaiseError("JIT compiler permanently disabled by build option")
	}
	return 0
}

func jitOff(L *LState) int {
	jitSetMode(L)
	return 0
}

func jitStatus(L *LState) int {
	L.Push(LFalse)
	return 1
}

func jitAttach(L *LState) int {
	L.CheckFunction(1)
	if L.Get(2) != LNil {
		L.CheckString(2)
	}
	return 0
}

/* }}} */

/* table {{{ */

// LuaJIT's table limits (lj_def.h: LJ_MAX_ASIZE, LJ_MAX_HBITS) and the
// largest preallocation table.new makes here (a hint only).
const (
	jitMaxASize    = 1<<27 + 1
	jitMaxHBits    = 26
	jitMaxPrealloc = 1 << 20
)

// jitTableNew is table.new(narray, nhash): a table preallocated for that
// many array and hash elements ("table overflow" beyond LuaJIT's limits).
func jitTableNew(L *LState) int {
	a, h := jitCheckInt(L, 1), uint32(jitCheckInt(L, 2))
	asize := uint32(0) // lj_tab_new_ah's sizes: a+1 array slots, 2^hbits nodes
	if a > 0 {
		asize = uint32(a) + 1
	}
	if asize > jitMaxASize || h > 1 && bits.Len32(h-1) > jitMaxHBits {
		L.Error(LString("table overflow"), 0) // lj_err_msg: no position from a Go function
	}
	L.Push(L.CreateTable(int(min(asize, jitMaxPrealloc)), int(min(h, jitMaxPrealloc))))
	return 1
}

// jitTableClear is table.clear(t): removes every key of t (raw).
func jitTableClear(L *LState) int {
	tb := L.CheckTable(1)
	for k, _ := tb.Next(LNil); k != LNil; k, _ = tb.Next(k) {
		tb.RawSet(k, LNil)
	}
	return 0
}

// jitTableMove is LuaJIT 2.1's table.move(a1, f, e, t [, a2]), a Lua
// function in lib_table.c compiled with raw table accesses (TGETR/TSETR,
// whose number keys are converted to int32 like a C cast) and number
// arguments as they are.
func jitTableMove(L *LState) int {
	a1 := L.CheckTable(1)
	f := float64(L.CheckNumber(2))
	e := float64(L.CheckNumber(3))
	t := float64(L.CheckNumber(4))
	a2 := a1
	if L.Get(5) != LNil {
		a2 = L.CheckTable(5)
	}
	move := func(i, d float64) {
		a2.RawSetInt(int(jitNum2Int(i+d)), a1.RawGetInt(int(jitNum2Int(i))))
	}
	if e >= f {
		d := t - f
		if t > e || t <= f || a1 != a2 {
			for i := f; i <= e; i++ {
				move(i, d)
			}
		} else {
			for i := e; i >= f; i-- {
				move(i, d)
			}
		}
	}
	L.Push(a2)
	return 1
}

/* }}} */

/* math {{{ */

// jitMathLog is math.log(x [, base]): log2(x) * (1 / log2(base)), as
// LuaJIT computes it.
func jitMathLog(L *LState) int {
	x := float64(L.CheckNumber(1))
	if L.GetTop() < 2 {
		L.Push(LNumber(math.Log(x)))
		return 1
	}
	y := float64(L.CheckNumber(2))
	L.Push(LNumber(jitLog2(x) * (1 / jitLog2(y))))
	return 1
}

// jitLog2 is log2, correctly rounded but for the rarest near-ties, as
// glibc's log2 is (Go's math.Log2 can be a unit in the last place off,
// which shows in math.log(x, base)): ln(m) = 2 atanh((m-1)/(m+1)) for the
// mantissa m in [sqrt(1/2), sqrt(2)), summed in double-double arithmetic.
func jitLog2(x float64) float64 {
	if !(x > 0) || math.IsInf(x, 1) {
		return math.Log2(x)
	}
	m, k := math.Frexp(x)
	if m < math.Sqrt2/2 {
		m *= 2
		k--
	}
	if m == 1 {
		return float64(k)
	}
	s := ddDiv(dd{m - 1, 0}, ddTwoSum(m, 1)) // m - 1 is exact
	s2 := ddMul(s, s)
	sum, term := s, s
	for n := 3.0; ; n += 2 {
		term = ddMul(term, s2)
		t := ddDiv(term, dd{n, 0})
		sum = ddAdd(sum, t)
		if math.Abs(t.hi) < 1e-36 {
			break
		}
	}
	ln := ddMul(sum, dd{2, 0})
	r := ddAdd(ddMul(ln, dd{1.4426950408889634, 2.0355273740931033e-17}), dd{float64(k), 0})
	return r.hi + r.lo
}

// dd is a double-double number, hi + lo with |lo| <= ulp(hi)/2.
type dd struct{ hi, lo float64 }

func ddTwoSum(a, b float64) dd {
	s := a + b
	bb := s - a
	return dd{s, (a - (s - bb)) + (b - bb)}
}

func ddQuickTwoSum(a, b float64) dd {
	s := a + b
	return dd{s, b - (s - a)}
}

func ddAdd(x, y dd) dd {
	s := ddTwoSum(x.hi, y.hi)
	t := ddTwoSum(x.lo, y.lo)
	s = ddQuickTwoSum(s.hi, s.lo+t.hi)
	return ddQuickTwoSum(s.hi, s.lo+t.lo)
}

func ddMul(x, y dd) dd {
	p := x.hi * y.hi
	e := math.FMA(x.hi, y.hi, -p) + (x.hi*y.lo + x.lo*y.hi)
	return ddQuickTwoSum(p, e)
}

func ddDiv(x, y dd) dd {
	q1 := x.hi / y.hi
	r := ddAdd(x, ddMul(y, dd{-q1, 0}))
	q2 := r.hi / y.hi
	r = ddAdd(r, ddMul(y, dd{-q2, 0}))
	q3 := r.hi / y.hi
	return ddAdd(ddQuickTwoSum(q1, q2), dd{q3, 0})
}

// jitMathMinMax is math.min/math.max as LuaJIT's minsd/maxsd: the running
// result is replaced by the next argument unless it is less (min) or
// greater (max) than it, so NaNs and signed zeros come out as in LuaJIT.
func jitMathMinMax(isMin bool) LGFunction {
	return func(L *LState) int {
		r := float64(L.CheckNumber(1))
		for i := 2; i <= L.GetTop(); i++ {
			x := float64(L.CheckNumber(i))
			if isMin && !(r < x) || !isMin && !(r > x) {
				r = x
			}
		}
		L.Push(LNumber(r))
		return 1
	}
}

/* }}} */

/* string {{{ */

// jitMaxStr is LJ_MAX_STR, the longest string LuaJIT makes.
const jitMaxStr = 0x7fffff00

// jitStrRep is LuaJIT 2.1's string.rep(s, n [, sep]).
func jitStrRep(L *LState) int {
	s := L.CheckString(1)
	n := jitCheckInt(L, 2)
	sep, hasSep := jitOptString(L, 3)
	if n <= 0 {
		L.Push(emptyLString)
		return 1
	}
	if !hasSep || n == 1 {
		sep = ""
	}
	if total := int64(len(s))*int64(n) + int64(len(sep))*int64(n-1); total > jitMaxStr {
		L.Error(LString("not enough memory"), 0)
	}
	var b strings.Builder
	b.Grow(len(s)*int(n) + len(sep)*int(n-1))
	for i := int32(0); i < n; i++ {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(s)
	}
	L.Push(LString(b.String()))
	return 1
}

/* }}} */

/* coroutine {{{ */

// jitCoIsYieldable is coroutine.isyieldable(): whether coroutine.yield
// would succeed here, in a coroutine and not under a Go function that calls
// Lua (pcall, a metamethod, an iterator, a sort comparison, ...). LuaJIT can
// also yield across pcall and metamethods; this VM cannot.
func jitCoIsYieldable(L *LState) int {
	L.Push(LBool(L != L.G.MainThread && L.Parent != nil && L.nCcalls == 0))
	return 1
}

/* }}} */

/* package {{{ */

// jitSearchPath is package.searchpath(name, path [, sep [, rep]]): the
// first readable file of the templates of path with the "?" replaced by
// name (sep, default ".", replaced by rep, default "/"), or nil and the
// list of the files tried.
func jitSearchPath(L *LState) int {
	// the arguments are checked from the last, in gcc's order of evaluation
	// of searchpath's arguments
	rep := L.OptString(4, string(os.PathSeparator))
	sep := L.OptString(3, ".")
	path := L.CheckString(2)
	name := L.CheckString(1)
	if sep != "" {
		name = strings.ReplaceAll(name, sep, rep)
	}
	var msg strings.Builder
	for _, tmpl := range strings.Split(path, ";") {
		if tmpl == "" {
			continue
		}
		filename := strings.ReplaceAll(tmpl, "?", name)
		if f, err := os.Open(filename); err == nil {
			_ = f.Close()
			L.Push(LString(filename))
			return 1
		}
		msg.WriteString("\n\tno file '" + filename + "'")
	}
	L.Push(LNil)
	L.Push(LString(msg.String()))
	return 2
}

/* }}} */

/* debug {{{ */

const upvalueIDsKey = "go-moonscript.upvalueids"

// jitUpvalueID is debug.upvalueid(f, n): a userdata that identifies the
// n-th upvalue of f (the same for upvalues shared by closures). The
// identities are kept in the registry for the life of the state.
func jitUpvalueID(L *LState) int {
	fn := L.CheckFunction(1)
	n := int(jitCheckInt(L, 2)) - 1
	if n < 0 || n >= len(fn.Upvalues) {
		L.ArgError(2, "index out of range")
	}
	reg := L.Get(RegistryIndex).(*LTable)
	ud, ok := reg.RawGetString(upvalueIDsKey).(*LUserData)
	if !ok {
		ud = L.NewUserData()
		ud.Value = map[*Upvalue]*LUserData{}
		reg.RawSetString(upvalueIDsKey, ud)
	}
	ids := ud.Value.(map[*Upvalue]*LUserData)
	uv := fn.Upvalues[n]
	id, ok := ids[uv]
	if !ok {
		id = L.NewUserData()
		ids[uv] = id
	}
	L.Push(id)
	return 1
}

// jitUpvalueJoin is debug.upvaluejoin(f1, n1, f2, n2): the n1-th upvalue
// of the Lua function f1 becomes the n2-th upvalue of the Lua function f2.
func jitUpvalueJoin(L *LState) int {
	var fns [2]*LFunction
	var idx [2]int
	for i := range 2 {
		fns[i] = L.CheckFunction(2*i + 1)
		if fns[i].IsG {
			L.ArgError(2*i+1, "Lua function expected")
		}
		idx[i] = int(jitCheckInt(L, 2*i+2)) - 1
		if idx[i] < 0 || idx[i] >= len(fns[i].Upvalues) {
			L.ArgError(2*i+2, "index out of range")
		}
	}
	fns[0].Upvalues[idx[0]] = fns[1].Upvalues[idx[1]]
	return 0
}

// jitGetInfo adds the "u" fields of LuaJIT 2.1 (Lua 5.2), nparams and
// isvararg, to debug.getinfo.
func jitGetInfo(getinfo LGFunction) LGFunction {
	return func(L *LState) int {
		what := L.OptString(2, "Slunf")
		if !strings.Contains(what, "u") || strings.HasPrefix(what, ">") {
			return getinfo(L)
		}
		withFunc := strings.Contains(what, "f")
		if !withFunc {
			L.SetTop(max(L.GetTop(), 2))
			L.Replace(2, LString(what+"f"))
		}
		n := getinfo(L)
		info, ok := L.Get(-1).(*LTable)
		if n != 1 || !ok {
			return n
		}
		if fn, ok := info.RawGetString("func").(*LFunction); ok {
			if fn.IsG || fn.Proto == nil {
				info.RawSetString("nparams", LNumber(0))
				info.RawSetString("isvararg", LTrue)
			} else {
				info.RawSetString("nparams", LNumber(fn.Proto.NumParameters))
				info.RawSetString("isvararg", LBool(fn.Proto.IsVarArg != 0))
			}
		}
		if !withFunc {
			info.RawSetString("func", LNil)
		}
		return 1
	}
}

// jitGetLocal adds LuaJIT 2.1's debug.getlocal(f, n) to debug.getlocal: the
// name of the n-th parameter of the Lua function f (nil for none).
func jitGetLocal(getlocal LGFunction) LGFunction {
	return func(L *LState) int {
		fn, ok := L.Get(1).(*LFunction)
		if !ok {
			return getlocal(L)
		}
		n := int(jitCheckInt(L, 2))
		if !fn.IsG && fn.Proto != nil && n >= 1 && n <= int(fn.Proto.NumParameters) && n <= len(fn.Proto.DbgLocals) {
			L.Push(LString(fn.Proto.DbgLocals[n-1].Name))
		} else {
			L.Push(LNil)
		}
		return 1
	}
}

/* }}} */

/* os {{{ */

// JITDateFormat is the format of os.date for LuaJIT, which passes it whole
// to glibc's strftime: the E and O modifiers are dropped before the
// conversions glibc accepts them for (the C locale has no alternative
// representations), so that the per-conversion strftime of Lua 5.1 gives
// glibc's result; elsewhere they stay, and are copied as they are.
func JITDateFormat(cfmt string) string {
	if !strings.Contains(cfmt, "%E") && !strings.Contains(cfmt, "%O") {
		return cfmt
	}
	var b strings.Builder
	for i := 0; i < len(cfmt); i++ {
		b.WriteByte(cfmt[i])
		if cfmt[i] != '%' || i+1 >= len(cfmt) {
			continue
		}
		i++
		if i+1 < len(cfmt) {
			switch {
			case cfmt[i] == 'E' && strings.IndexByte("cCnpPrRstTuxXyYzZ%", cfmt[i+1]) >= 0,
				cfmt[i] == 'O' && strings.IndexByte("bBCdegGhHIjklmMnpPrRsStTuUVwWyzZ%", cfmt[i+1]) >= 0:
				i++
			}
		}
		b.WriteByte(cfmt[i])
	}
	return b.String()
}

/* }}} */
