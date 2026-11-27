package parse

// go-moonscript patch: the parser is Lua 5.1's lparser.c (Lua51) or LuaJIT
// 2.1's lj_parse.c (LuaJIT), recursive descent, ported to Go for what they
// accept and the errors they give (the code generation is compile.go's).
// It builds the syntax tree gopher-lua's yacc grammar built, with the same
// lines. Like luac, it checks the limits of LUAI_MAXCCALLS (200 syntax
// levels), LUAI_MAXVARS (200 local variables per function) and
// LUAI_MAXUPVALUES (60 upvalues per function), which are also LuaJIT's.
// Under LuaJIT, it also emits LuaJIT's bytecode as lj_parse.c does
// (jitcode.go, with the expression descriptors the expression functions
// pass around), for the limits of LuaJIT's variable stack and code
// generator. Under Lua51, Lua51Overflow parses with a model of Lua 5.1's
// register allocation instead (lua51regs.go, p.r51), for the chunks the
// compiler needs many registers for.

import (
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"sync"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
)

const (
	maxLevels   = 200 // LUAI_MAXCCALLS, LJ_MAX_XLEVEL
	maxVars     = 200 // LUAI_MAXVARS, LJ_MAX_LOCVAR
	maxUpvalues = 60  // LUAI_MAXUPVALUES, LJ_MAX_UPVAL
	// lua51BaseLevel is Lua 5.1's L->nCcalls when loadstring is called from
	// Lua code: the C calls that its syntax levels add to.
	lua51BaseLevel = 2
)

// Parse parses a Lua 5.1 chunk.
func Parse(reader io.Reader, name string) (chunk []ast.Stmt, err error) {
	return ParseDialect(reader, name, Lua51)
}

// ParseDialect parses a chunk of the dialect. A syntax error is an *Error.
func ParseDialect(reader io.Reader, name string, dialect Dialect) (chunk []ast.Stmt, err error) {
	src, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return ParseBytes(src, name, dialect)
}

// ParseBytes parses a chunk of the dialect held in memory. A syntax error
// is an *Error.
func ParseBytes(src []byte, name string, dialect Dialect) (chunk []ast.Stmt, err error) {
	p := &parser{jit: dialect == LuaJIT}
	sc := scratchPool.Get().(*scratch)
	p.fsCache, p.bcstack, p.lnstack = sc.fsCache, sc.bcstack, sc.lnstack
	defer func() {
		p.release(sc)
		if e := recover(); e != nil {
			pe, ok := e.(*Error)
			if !ok {
				panic(e)
			}
			chunk, err = nil, pe
		}
	}()
	p.ls = newLexer(src, name, dialect) // it may raise an error
	if !p.jit {
		p.level = lua51BaseLevel
	}
	p.openFunc(0, true)
	if p.jit {
		p.fs.emit(p, insAD(bcFUNCV, 0, 0)) // the header's place
	}
	p.ls.nextToken()
	chunk = p.chunk()
	p.check(tkEOS)
	p.closeFunc()
	return chunk, nil
}

// scratch is the memory a parser passes on to the next chunks: the states
// of the functions by depth, LuaJIT's bytecode stack.
type scratch struct {
	fsCache []*funcState
	bcstack []bcIns
	lnstack []int32
}

var scratchPool = sync.Pool{New: func() any { return &scratch{} }}

// release returns the parser's memory to the pool, without references to
// the chunk.
func (p *parser) release(sc *scratch) {
	for _, fs := range p.fsCache {
		clear(fs.actvar[:cap(fs.actvar)])
		clear(fs.pending[:cap(fs.pending)])
		clear(fs.upvals[:cap(fs.upvals)])
		clear(fs.vstack[:cap(fs.vstack)])
		fs.reset()
	}
	sc.fsCache, sc.bcstack, sc.lnstack = p.fsCache, nil, nil
	if len(p.bcstack) <= maxPooledBytecode {
		sc.bcstack, sc.lnstack = p.bcstack, p.lnstack
	}
	scratchPool.Put(sc)
}

// newFuncState returns a cleared state for a function at the current
// depth: the functions at a depth reuse one state and its memory.
func (p *parser) newFuncState() *funcState {
	if p.depth == len(p.fsCache) {
		p.fsCache = append(p.fsCache, &funcState{})
	}
	fs := p.fsCache[p.depth]
	fs.reset()
	p.depth++
	return fs
}

// reset clears the state and keeps its slices and maps, or drops the maps
// grown big.
func (fs *funcState) reset() {
	kn, kstr := fs.kn, fs.kstr
	if len(kn) > 1024 {
		kn = nil
	} else if len(kn) > 0 {
		clear(kn)
	}
	if len(kstr) > 1024 {
		kstr = nil
	} else if len(kstr) > 0 {
		clear(kstr)
	}
	*fs = funcState{
		actvar:  fs.actvar[:0],
		pending: fs.pending[:0],
		upvals:  fs.upvals[:0],
		vstack:  fs.vstack[:0],
		jitFunc: jitFunc{kn: kn, kstr: kstr},
	}
}

type varInfo struct {
	name string
	id   int
}

type blockScope struct {
	prev    *blockScope
	nactvar int
	isLoop  bool
	// LuaJIT's FuncScope: where its gotos and labels start in vstack, and
	// whether it has breaks or gotos and labels to resolve when it ends
	vstart   int
	hasBreak bool
	hasGola  bool
	// LuaJIT's FSCOPE_UPVAL (a local of the block is an upvalue) and
	// FSCOPE_NOCLOSE (its upvalues are closed already)
	upval   bool
	noClose bool
}

// gola is a goto or label of LuaJIT's (lj_parse.c's VarInfo): the number
// of active local variables where it is, the line of a goto, and its
// instruction: the jump of a goto, the target of a label.
type gola struct {
	name    string
	isLabel bool
	slot    int
	line    int
	valid   bool
	pc      uint32
}

// breakName is a break's name as a goto (no Lua name has a NUL).
const breakName = "\x00break"

type funcState struct {
	prev    *funcState
	line    int // linedefined, 0 for the main function
	vararg  bool
	actvar  []varInfo // the active local variables
	pending []varInfo // declared, not active yet
	nlocvar int       // Lua 5.1: the local variable records (f->locvars)
	upvals  []varInfo // the variables of the upvalues
	bl      *blockScope
	vstack  []gola // LuaJIT
	jitFunc        // LuaJIT's code generator (jitcode.go)
}

type parser struct {
	ls     *lexer
	fs     *funcState
	jit    bool
	r51    bool // the model of Lua 5.1's registers runs (Lua51Overflow)
	level  int
	nextID int
	// LuaJIT's code generator: the bytecode of the functions being parsed,
	// the number of entries of the variable stack (ls->vtop), the targets
	// of the assignments being parsed
	bcstack []bcIns
	lnstack []int32
	vtop    int
	lhs     []expDesc
	// the states of the functions by depth (newFuncState), the number of
	// functions being parsed
	fsCache []*funcState
	depth   int
}

// expression kinds, for the statements
const (
	kindOther = iota
	kindVar   // a name or an indexing: assignable
	kindCall  // a function call (not in parentheses)
	kindParen // in parentheses
)

/* helpers */

func (p *parser) next() { p.ls.nextToken() }

func (p *parser) errorExpected(tok int) {
	p.ls.syntaxError(fmt.Sprintf("'%s' expected", p.ls.token2str(tok)))
}

func (p *parser) testNext(c int) bool {
	if p.ls.t.typ == c {
		p.next()
		return true
	}
	return false
}

func (p *parser) check(c int) {
	if p.ls.t.typ != c {
		p.errorExpected(c)
	}
}

func (p *parser) checkNext(c int) {
	p.check(c)
	p.next()
}

func (p *parser) checkMatch(what, who, where int) {
	if !p.testNext(what) {
		if where == p.ls.linenumber {
			p.errorExpected(what)
		}
		p.ls.syntaxError(fmt.Sprintf("'%s' expected (to close '%s' at line %d)",
			p.ls.token2str(what), p.ls.token2str(who), where))
	}
}

// isName: LuaJIT's goto is a name where a name is expected.
func (p *parser) isName() bool {
	return p.ls.t.typ == tkName || p.ls.t.typ == tkGoto
}

// strCheckName is str_checkname / lex_str: the name and its line.
func (p *parser) strCheckName() (string, int) {
	if !p.isName() {
		p.errorExpected(tkName)
	}
	name, line := p.ls.t.sem, p.ls.t.line
	p.next()
	return name, line
}

func (p *parser) errorLimit(fs *funcState, limit int, what string) {
	var msg string
	if fs.line == 0 {
		msg = fmt.Sprintf("main function has more than %d %s", limit, what)
	} else {
		msg = fmt.Sprintf("function at line %d has more than %d %s", fs.line, limit, what)
	}
	p.ls.lexError(msg, 0)
}

func (p *parser) enterLevel() {
	p.level++
	if p.level > maxLevels || p.jit && p.level >= maxLevels {
		p.ls.lexError("chunk has too many syntax levels", 0)
	}
}

func (p *parser) leaveLevel() { p.level-- }

/* variables and scopes */

func (p *parser) openFunc(line int, vararg bool) {
	fs := p.newFuncState()
	fs.prev, fs.line, fs.vararg = p.fs, line, vararg
	p.fs = fs
	if p.jit {
		p.jitOpenFunc(fs)
		if vararg {
			fs.flags |= protoVararg
		}
	}
	p.enterBlock(false)
}

func (p *parser) closeFunc() {
	if p.jit {
		p.jitCloseFunc()
	} else {
		p.leaveBlock()
	}
	p.fs = p.fs.prev
	p.depth--
}

func (p *parser) enterBlock(isLoop bool) {
	fs := p.fs
	fs.bl = &blockScope{prev: fs.bl, nactvar: len(fs.actvar), isLoop: isLoop, vstart: len(fs.vstack)}
}

// leaveBlock is fscope_end: under LuaJIT, it closes the upvalues of the
// block, resolves the breaks of a loop, and the gotos and labels of the
// block, or passes them on.
func (p *parser) leaveBlock() {
	fs := p.fs
	bl := fs.bl
	fs.actvar = fs.actvar[:bl.nactvar]
	fs.bl = bl.prev
	if p.r51 {
		fs.freereg = fs.nactvar()
	}
	if p.jit {
		fs.freereg = fs.nactvar()
		if bl.upval && !bl.noClose {
			fs.emit(p, insAJ(bcUCLO, uint32(bl.nactvar), 0))
		}
	}
	if bl.hasBreak {
		if bl.isLoop {
			idx := p.golaNew(breakName, true, 0, fs.pc)
			if p.jit {
				p.vtop-- // the break label is dropped at once
			}
			p.golaResolve(bl, idx)
			fs.vstack = fs.vstack[:idx]
		} else {
			p.golaFixup(bl)
			return
		}
	}
	if bl.hasGola {
		p.golaFixup(bl)
	}
}

/* LuaJIT's gotos and labels */

func (p *parser) golaNew(name string, isLabel bool, line int, pc uint32) int {
	fs := p.fs
	if p.jit {
		p.vstackPush()
	}
	fs.vstack = append(fs.vstack, gola{name: name, isLabel: isLabel, slot: len(fs.actvar), line: line, valid: true, pc: pc})
	return len(fs.vstack) - 1
}

// golaFindLabel returns the index of the label in the current block, or -1.
func (p *parser) golaFindLabel(name string) int {
	fs := p.fs
	for i := fs.bl.vstart; i < len(fs.vstack); i++ {
		if v := &fs.vstack[i]; v.valid && v.isLabel && v.name == name {
			return i
		}
	}
	return -1
}

// golaResolve resolves the pending gotos of the block to the label at idx.
func (p *parser) golaResolve(bl *blockScope, idx int) {
	fs := p.fs
	vl := fs.vstack[idx]
	for i := bl.vstart; i < idx; i++ {
		vg := &fs.vstack[i]
		if vg.valid && !vg.isLabel && vg.name == vl.name {
			if vg.slot < vl.slot {
				p.ls.errorAt(vg.line, fmt.Sprintf("<goto %s> jumps into the scope of local '%s'",
					vg.name, fs.actvar[vg.slot].name), 0)
			}
			p.golaPatch(vg, &vl)
		}
	}
}

// golaPatch makes a goto jump to a label (gola_patch).
func (p *parser) golaPatch(vg, vl *gola) {
	vg.valid = false
	if p.jit {
		fs := p.fs
		fs.bc[vg.pc].setA(uint32(vl.slot))
		fs.jmpPatch(p, vg.pc, vl.pc)
	}
}

// golaClose makes a goto out of a block close its upvalues (gola_close).
func (p *parser) golaClose(vg *gola) {
	fs := p.fs
	ip := &fs.bc[vg.pc]
	ip.setA(uint32(vg.slot))
	if ip.op() == bcJMP {
		if next := fs.jmpNext(vg.pc); next != jitNoJump {
			fs.jmpPatch(p, next, vg.pc) // to the UCLO
		}
		ip = &fs.bc[vg.pc]
		ip.setOp(bcUCLO)
		ip.setJ(-1) // NO_JMP
	}
}

// golaFixup ends the labels of the block, resolving the gotos back to them,
// and passes its pending gotos on to the enclosing block: an error in the
// function's.
func (p *parser) golaFixup(bl *blockScope) {
	fs := p.fs
	for i := bl.vstart; i < len(fs.vstack); i++ {
		v := &fs.vstack[i]
		if !v.valid {
			continue
		}
		if v.isLabel {
			v.valid = false
			for j := i + 1; j < len(fs.vstack); j++ {
				if vg := &fs.vstack[j]; vg.valid && !vg.isLabel && vg.name == v.name {
					if p.jit && bl.upval && vg.slot > v.slot {
						p.golaClose(vg)
					}
					p.golaPatch(vg, v)
				}
			}
		} else if bl.prev != nil {
			if v.name == breakName {
				bl.prev.hasBreak = true
			} else {
				bl.prev.hasGola = true
			}
			v.slot = bl.nactvar
			if p.jit && bl.upval {
				p.golaClose(v)
			}
		} else if v.name == breakName {
			p.ls.errorAt(v.line, "no loop to break", 0)
		} else {
			p.ls.errorAt(v.line, fmt.Sprintf("undefined label '%s'", v.name), 0)
		}
	}
}

// maxLocVars is Lua 5.1's limit of local variable records per function:
// registerlocalvar grows f->locvars by luaM_growvector up to SHRT_MAX.
const maxLocVars = 32767

// newLocalVar declares the n-th variable of a declaration.
func (p *parser) newLocalVar(name string, n int) {
	fs := p.fs
	if len(fs.actvar)+n+1 > maxVars {
		p.errorLimit(fs, maxVars, "local variables")
	}
	if p.jit {
		p.vstackPush()
	} else {
		if fs.nlocvar >= maxLocVars {
			p.ls.runError("too many local variables") // luaM_growaux_'s
		}
		fs.nlocvar++
	}
	p.nextID++
	fs.pending = append(fs.pending[:n], varInfo{name, p.nextID})
}

// adjustLocalVars activates the first n variables declared.
func (p *parser) adjustLocalVars(n int) {
	fs := p.fs
	fs.actvar = append(fs.actvar, fs.pending[:n]...)
	fs.pending = append(fs.pending[:0], fs.pending[n:]...)
}

// singleVar resolves a name, making it an upvalue of the functions it is
// referenced from, as singlevaraux / var_lookup do; under LuaJIT, e is the
// local, upvalue or global, and a local referenced from a function inside
// its block marks the block (fscope_uvmark).
func (p *parser) singleVar(fs *funcState, name string, e *expDesc, first bool) (int, bool) {
	if fs == nil {
		if p.jit || p.r51 {
			e.init(vGlobal, 0)
			e.sval = name
		}
		return 0, false // a global
	}
	for i := len(fs.actvar) - 1; i >= 0; i-- {
		if fs.actvar[i].name == name {
			if p.jit || p.r51 {
				e.init(vLocal, uint32(i))
			}
			if p.jit {
				if !first {
					bl := fs.bl
					for bl != nil && bl.nactvar > i {
						bl = bl.prev
					}
					if bl != nil {
						bl.upval = true
					}
				}
			}
			return fs.actvar[i].id, true
		}
	}
	id, ok := p.singleVar(fs.prev, name, e, false)
	if !ok {
		return 0, false
	}
	idx := -1
	for i, u := range fs.upvals {
		if u.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		if len(fs.upvals)+1 > maxUpvalues {
			p.errorLimit(fs, maxUpvalues, "upvalues")
		}
		idx = len(fs.upvals)
		fs.upvals = append(fs.upvals, varInfo{name, id})
	}
	if p.jit || p.r51 {
		e.info, e.k = uint32(idx), vUpval
	}
	return id, true
}

/* statements */

func blockFollow(tok int) bool {
	switch tok {
	case tkElse, tkElseif, tkEnd, tkUntil, tkEOS:
		return true
	}
	return false
}

func (p *parser) chunk() []ast.Stmt {
	stmts := []ast.Stmt{}
	p.enterLevel()
	for islast := false; !islast && !blockFollow(p.ls.t.typ); {
		islast = p.statement(&stmts)
		p.testNext(';')
		if p.jit || p.r51 {
			p.fs.freereg = p.fs.nactvar() // the registers are free after each statement
		}
	}
	p.leaveLevel()
	return stmts
}

func (p *parser) block() []ast.Stmt {
	p.enterBlock(false)
	stmts := p.chunk()
	p.leaveBlock()
	return stmts
}

// cond is expr_cond under LuaJIT: the expression, and the jumps taken
// when it is false.
func (p *parser) cond() (ast.Expr, uint32) {
	var v expDesc
	e, _ := p.expr(&v)
	if p.r51 {
		if v.k == vKNil {
			v.k = vKFalse
		}
		p.fs.rGoIfTrue(p, &v)
	}
	if !p.jit {
		return e, 0
	}
	if v.k == vKNil {
		v.k = vKFalse
	}
	p.fs.branchT(p, &v)
	return e, v.f
}

// statement parses a statement into stmts; it reports whether the
// statement must be the last one of its block.
func (p *parser) statement(stmts *[]ast.Stmt) bool {
	ls := p.ls
	fs := p.fs
	line := ls.linenumber
	tokLine := ls.t.line
	add := func(s ast.Stmt) { *stmts = append(*stmts, s) }
	switch ls.t.typ {
	case tkIf:
		add(p.ifStat(line))
	case tkWhile:
		p.next()
		start := fs.pc
		fs.lasttarget = start
		cond, condexit := p.cond()
		p.enterBlock(true)
		p.checkNext(tkDo)
		var loop uint32
		if p.jit {
			loop = fs.emit(p, insAD(bcLOOP, fs.nactvar(), 0))
		}
		body := p.block()
		if p.jit {
			fs.jmpPatch(p, fs.jmp(p), start)
		}
		endLine := ls.t.line
		p.checkMatch(tkEnd, tkWhile, line)
		p.leaveBlock()
		if p.jit {
			fs.jmpToHere(p, condexit)
			fs.jmpPatchIns(p, loop, fs.pc)
		}
		st := &ast.WhileStmt{Condition: cond, Stmts: body}
		st.SetLine(tokLine)
		st.SetLastLine(endLine)
		add(st)
	case tkDo:
		p.next()
		body := p.block()
		endLine := ls.t.line
		p.checkMatch(tkEnd, tkDo, line)
		st := &ast.DoBlockStmt{Stmts: body}
		st.SetLine(tokLine)
		st.SetLastLine(endLine)
		add(st)
	case tkFor:
		add(p.forStat(line))
	case tkRepeat:
		loop := fs.pc
		fs.lasttarget = loop
		p.enterBlock(true)  // the loop
		p.enterBlock(false) // the scope
		inner := fs.bl
		p.next()
		if p.jit {
			fs.emit(p, insAD(bcLOOP, fs.nactvar(), 0))
		}
		body := p.chunk()
		p.checkMatch(tkUntil, tkRepeat, line)
		cond, condexit := p.cond() // in the scope
		if !p.jit || !inner.upval {
			p.leaveBlock()
		} else { // UCLO and a jump out if true, UCLO and a jump back if not
			p.breakStat() // out of the loop before the scope ends
			fs.jmpToHere(p, condexit)
			p.leaveBlock()
			condexit = fs.jmp(p)
		}
		if p.jit {
			fs.jmpPatch(p, condexit, loop) // back if false
			fs.jmpPatchIns(p, loop, fs.pc)
		}
		p.leaveBlock()
		st := &ast.RepeatStmt{Condition: cond, Stmts: body}
		st.SetLine(tokLine)
		st.SetLastLine(cond.Line())
		add(st)
	case tkFunction:
		add(p.funcStat(line))
	case tkLocal:
		p.next()
		if p.testNext(tkFunction) {
			name, _ := p.strCheckName()
			p.newLocalVar(name, 0)
			var lv, b expDesc
			if p.jit {
				lv.init(vLocal, fs.freereg)
				fs.regReserve(p, 1)
			} else if p.r51 {
				lv.init(vLocal, fs.freereg)
				fs.rReserve(p, 1)
			}
			p.adjustLocalVars(1)
			fn := p.body(&b, false, ls.linenumber)
			if p.jit {
				fs.exprFree(&b)
				fs.toReg(p, &b, lv.info)
			} else if p.r51 {
				fs.rStoreVar(p, &lv, &b)
			}
			st := &ast.LocalAssignStmt{Names: []string{name}, Exprs: []ast.Expr{fn}, IsFunction: true}
			st.SetLine(tokLine)
			st.SetLastLine(fn.LastLine())
			add(st)
		} else {
			add(p.localStat(tokLine))
		}
	case tkReturn:
		add(p.returnStat(tokLine))
		return true
	case tkBreak:
		p.next()
		if p.jit { // a goto, resolved when the loop ends
			p.breakStat()
		} else {
			bl := p.fs.bl
			for bl != nil && !bl.isLoop {
				bl = bl.prev
			}
			if bl == nil {
				ls.syntaxError("no loop to break")
			}
		}
		st := &ast.BreakStmt{}
		st.SetLine(tokLine)
		add(st)
		return true
	case tkLabel:
		p.labelStat(stmts)
	case tkGoto:
		if ls.lookahead() == tkName {
			p.next()
			name, _ := p.strCheckName()
			if l := p.golaFindLabel(name); l >= 0 { // a backward goto in the scope is a loop
				fs.emit(p, insAJ(bcLOOP, uint32(fs.vstack[l].slot), -1))
			}
			p.fs.bl.hasGola = true
			pc := fs.jmp(p)
			p.golaNew(name, false, int(fs.lines[pc]), pc)
			st := &ast.GotoStmt{Label: name}
			st.SetLine(tokLine)
			add(st)
			break
		}
		add(p.exprStat())
	default:
		add(p.exprStat())
	}
	return false
}

// breakStat is LuaJIT's parse_break: a goto to the end of the loop.
func (p *parser) breakStat() {
	fs := p.fs
	fs.bl.hasBreak = true
	pc := fs.jmp(p)
	p.golaNew(breakName, false, int(fs.lines[pc]), pc)
}

func (p *parser) returnStat(line int) ast.Stmt {
	ls := p.ls
	fs := p.fs
	p.next()
	var exprs []ast.Expr
	var ins bcIns
	if p.jit {
		fs.flags |= protoHasReturn
		ins = insAD(bcRET0, 0, 1)
	}
	if !blockFollow(ls.t.typ) && ls.t.typ != ';' {
		var e expDesc
		exprs = p.exprList(&e)
		if p.jit {
			ins = p.returnIns(&e, len(exprs))
		} else if p.r51 {
			p.rReturn(&e, len(exprs))
		}
	}
	if p.jit {
		if fs.flags&protoChild != 0 {
			fs.emit(p, insAJ(bcUCLO, 0, 0)) // may have upvalues to close first
		}
		fs.emit(p, ins)
	}
	st := &ast.ReturnStmt{Exprs: exprs}
	st.SetLine(line)
	return st
}

// returnIns is the instruction returning the values of a return, the last
// one in e (parse_return).
func (p *parser) returnIns(e *expDesc, nret int) bcIns {
	fs := p.fs
	if e.k == vCall {
		call := fs.bc[e.info]
		// a tail call; it does not pay off to add VARGT for "return ..."
		if nret == 1 && call.op() != bcVARG {
			fs.pc--
			return insAD(call.op()-bcCALL+bcCALLT, call.a(), call.c())
		}
		fs.bc[e.info].setB(0) // all the results of the call
		return insAD(bcRETM, fs.nactvar(), e.aux-fs.nactvar())
	}
	if nret == 1 { // the result from any register
		return insAD(bcRET1, fs.toAnyReg(p, e), 2)
	}
	fs.toNextReg(p, e) // in consecutive registers
	return insAD(bcRET, fs.nactvar(), uint32(nret+1))
}

func (p *parser) ifStat(line int) ast.Stmt {
	ls := p.ls
	fs := p.fs
	escape := jitNoJump
	ifLine := ls.t.line
	p.next()
	cond, flist := p.cond()
	p.checkNext(tkThen)
	top := &ast.IfStmt{Condition: cond, Then: p.block()}
	top.SetLine(ifLine)
	cur := top
	for ls.t.typ == tkElseif {
		if p.jit {
			fs.jmpAppend(p, &escape, fs.jmp(p))
			fs.jmpToHere(p, flist)
		}
		elseifLine := ls.t.line
		p.next()
		var cond ast.Expr
		cond, flist = p.cond()
		p.checkNext(tkThen)
		st := &ast.IfStmt{Condition: cond, Then: p.block()}
		st.SetLine(elseifLine)
		cur.Else = []ast.Stmt{st}
		cur = st
	}
	if ls.t.typ == tkElse {
		if p.jit {
			fs.jmpAppend(p, &escape, fs.jmp(p))
			fs.jmpToHere(p, flist)
		}
		p.next()
		cur.Else = p.block()
	} else if p.jit {
		fs.jmpAppend(p, &escape, flist)
	}
	if p.jit {
		fs.jmpToHere(p, escape)
	}
	top.SetLastLine(ls.t.line)
	p.checkMatch(tkEnd, tkIf, line)
	return top
}

func (p *parser) forStat(line int) ast.Stmt {
	ls := p.ls
	forLine := ls.t.line
	p.enterBlock(true)
	p.next()
	name, _ := p.strCheckName()
	var st ast.Stmt
	switch ls.t.typ {
	case '=':
		st = p.forNum(name, line)
	case ',', tkIn:
		st = p.forIter(name)
	default:
		ls.syntaxError("'=' or 'in' expected")
	}
	st.SetLine(forLine)
	st.SetLastLine(ls.t.line)
	p.checkMatch(tkEnd, tkFor, line)
	p.leaveBlock()
	return st
}

// forExp is a numeric for's expression, in the next register (exp1,
// expr_next).
func (p *parser) forExp() ast.Expr {
	var v expDesc
	e, _ := p.expr(&v)
	if p.jit {
		p.fs.toNextReg(p, &v)
	} else if p.r51 {
		p.fs.rExp2NextReg(p, &v)
	}
	return e
}

// forNum is fornum / parse_for_num: the loop instructions after the body.
func (p *parser) forNum(name string, line int) ast.Stmt {
	fs := p.fs
	base := fs.freereg
	p.newLocalVar("(for index)", 0)
	p.newLocalVar("(for limit)", 1)
	p.newLocalVar("(for step)", 2)
	p.newLocalVar(name, 3)
	p.next()
	init := p.forExp()
	p.checkNext(',')
	limit := p.forExp()
	var step ast.Expr
	if p.testNext(',') {
		step = p.forExp()
	} else if p.jit {
		fs.emit(p, insAD(bcKSHORT, fs.freereg, 1)) // the default step
		fs.regReserve(p, 1)
	} else if p.r51 {
		fs.rNumberK(1) // the default step
		fs.rReserve(p, 1)
	}
	p.adjustLocalVars(3) // the control variables
	p.checkNext(tkDo)
	var loop uint32
	if p.jit {
		loop = fs.emit(p, insAJ(bcFORI, base, -1))
	}
	stmts := p.forBody(1)
	if p.jit {
		loopend := fs.emit(p, insAJ(bcFORL, base, -1))
		fs.jmpPatchIns(p, loopend, loop+1)
		fs.jmpPatchIns(p, loop, fs.pc)
	}
	return &ast.NumberForStmt{Name: name, Init: init, Limit: limit, Step: step, Stmts: stmts}
}

// forIter is forlist / parse_for_iter.
func (p *parser) forIter(name string) ast.Stmt {
	fs := p.fs
	base := fs.freereg + 3
	exprpc := fs.pc
	p.newLocalVar("(for generator)", 0)
	p.newLocalVar("(for state)", 1)
	p.newLocalVar("(for control)", 2)
	p.newLocalVar(name, 3)
	names := []string{name}
	for p.testNext(',') {
		name, _ := p.strCheckName()
		p.newLocalVar(name, len(names)+3)
		names = append(names, name)
	}
	p.checkNext(tkIn)
	var e expDesc
	exprs := p.exprList(&e)
	isnext := false
	if p.jit {
		p.assignAdjust(3, len(exprs), &e)
		fs.regBump(p, 3+fr2) // the iterator takes another 3+1 slots
		isnext = len(names) <= 2 && fs.pc > exprpc && p.predictNext(exprpc)
	} else if p.r51 {
		p.rAdjustAssign(3, len(exprs), &e)
		fs.rCheckStack(p, 3) // to call the generator
	}
	p.adjustLocalVars(3) // the control variables
	p.checkNext(tkDo)
	var loop uint32
	if p.jit {
		op := bcJMP
		if isnext {
			op = bcISNEXT
		}
		loop = fs.emit(p, insAJ(op, base, -1))
	}
	stmts := p.forBody(len(names))
	if p.jit {
		fs.jmpPatchIns(p, loop, fs.pc)
		op := bcITERC
		if isnext {
			op = bcITERN
		}
		fs.emit(p, insABC(op, base, uint32(len(names))+1, 2+1))
		loopend := fs.emit(p, insAJ(bcITERL, base, -1))
		fs.jmpPatchIns(p, loopend, loop+1)
	}
	return &ast.GenericForStmt{Names: names, Exprs: exprs, Stmts: stmts}
}

// predictNext guesses from the first instruction of a generic for's
// iterator whether it is next() or pairs() (predict_next).
func (p *parser) predictNext(pc uint32) bool {
	fs := p.fs
	ins := fs.bc[pc]
	var name string
	switch ins.op() {
	case bcMOV:
		if ins.d() >= fs.nactvar() {
			return false
		}
		name = fs.actvar[ins.d()].name
	case bcUGET:
		name = fs.upvals[ins.d()].name
	case bcGGET:
		for _, s := range [...]string{"pairs", "next"} {
			if k, ok := fs.kstr[s]; ok && k == ins.d() {
				return true
			}
		}
		return false
	default:
		return false
	}
	return name == "pairs" || name == "next"
}

// forBody is the scope of a for's variables and its block.
func (p *parser) forBody(nvars int) []ast.Stmt {
	p.enterBlock(false)
	p.adjustLocalVars(nvars)
	if p.jit {
		p.fs.regReserve(p, uint32(nvars))
	} else if p.r51 {
		p.fs.rReserve(p, uint32(nvars))
	}
	stmts := p.block()
	p.leaveBlock()
	return stmts
}

func (p *parser) funcStat(line int) ast.Stmt {
	ls := p.ls
	fs := p.fs
	var v, b expDesc
	p.next()
	// funcname: NAME {'.' NAME} [':' NAME]
	name, nameLine := p.strCheckName()
	p.singleVar(fs, name, &v, true)
	if p.r51 && v.k == vGlobal {
		v.info = fs.rStringK(name)
	}
	var fn ast.Expr = &ast.IdentExpr{Value: name}
	fn.SetLine(nameLine)
	for ls.t.typ == '.' {
		key, keyLine := p.field(&v)
		k := &ast.StringExpr{Value: key}
		k.SetLine(keyLine)
		fn = &ast.AttrGetExpr{Object: fn, Key: k}
		fn.SetLine(keyLine)
	}
	funcname := &ast.FuncName{Func: fn}
	needself := false
	if ls.t.typ == ':' {
		needself = true
		method, _ := p.field(&v)
		funcname = &ast.FuncName{Receiver: fn, Method: method}
	}
	body := p.body(&b, needself, line)
	if p.jit {
		fs.store(p, &v, &b)
	} else if p.r51 {
		fs.rStoreVar(p, &v, &b)
	}
	st := &ast.FuncDefStmt{Name: funcname, Func: body}
	st.SetLine(line)
	st.SetLastLine(body.LastLine())
	return st
}

// field parses '.' NAME or ':' NAME, an index of v under LuaJIT
// (expr_field): the name and its line.
func (p *parser) field(v *expDesc) (string, int) {
	if p.jit {
		p.fs.toAnyReg(p, v)
	} else if p.r51 {
		p.fs.rExp2AnyReg(p, v)
	}
	p.next()
	name, line := p.strCheckName()
	if p.jit {
		var k expDesc
		k.init(vKStr, 0)
		k.sval = name
		p.fs.index(p, v, &k)
	} else if p.r51 {
		var k expDesc
		k.init(vKStr, p.fs.rStringK(name))
		p.fs.rIndexed(p, v, &k)
	}
	return name, line
}

func (p *parser) localStat(line int) ast.Stmt {
	var names []string
	for {
		name, _ := p.strCheckName()
		p.newLocalVar(name, len(names))
		names = append(names, name)
		if !p.testNext(',') {
			break
		}
	}
	exprs := []ast.Expr{}
	var e expDesc
	if p.testNext('=') {
		exprs = p.exprList(&e)
	} else {
		e.k = vVoid
	}
	if p.jit {
		p.assignAdjust(len(names), len(exprs), &e)
	} else if p.r51 {
		p.rAdjustAssign(len(names), len(exprs), &e)
	}
	p.adjustLocalVars(len(names))
	st := &ast.LocalAssignStmt{Names: names, Exprs: exprs}
	st.SetLine(line)
	return st
}

// assignAdjust adjusts the values of an assignment to its targets under
// LuaJIT (assign_adjust): a call returns as many results as needed,
// missing values are nil, extra ones dropped.
func (p *parser) assignAdjust(nvars, nexps int, e *expDesc) {
	fs := p.fs
	extra := nvars - nexps
	if e.k == vCall {
		extra++ // the call itself
		if extra < 0 {
			extra = 0
		}
		fs.bc[e.info].setB(uint32(extra + 1)) // the call's results
		if extra > 1 {
			fs.regReserve(p, uint32(extra-1))
		}
	} else {
		if e.k != vVoid {
			fs.toNextReg(p, e) // the last expression
		}
		if extra > 0 { // nil to the targets left
			reg := fs.freereg
			fs.regReserve(p, uint32(extra))
			fs.emitNil(p, reg, uint32(extra))
		}
	}
	if nexps > nvars {
		fs.freereg -= uint32(nexps - nvars) // drops the registers left
	}
}

// labelStat parses LuaJIT's '::' NAME '::' and the labels right after it.
func (p *parser) labelStat(stmts *[]ast.Stmt) {
	ls := p.ls
	line := ls.t.line
	fs := p.fs
	fs.lasttarget = fs.pc
	fs.bl.hasGola = true
	p.next()
	name, _ := p.strCheckName()
	if p.golaFindLabel(name) >= 0 {
		ls.lexError(fmt.Sprintf("duplicate label '%s'", name), 0)
	}
	idx := p.golaNew(name, true, 0, fs.pc)
	p.checkNext(tkLabel)
	st := &ast.LabelStmt{Name: name}
	st.SetLine(line)
	*stmts = append(*stmts, st)
	if ls.t.typ == tkLabel {
		p.enterLevel()
		p.labelStat(stmts)
		p.leaveLevel()
	}
	// a label at the end of a block is out of the scope of its locals
	if blockFollow(ls.t.typ) && ls.t.typ != tkUntil {
		fs.vstack[idx].slot = fs.bl.nactvar
	}
	p.golaResolve(fs.bl, idx)
}

// exprStat is exprstat / parse_call_assign. Under LuaJIT, the targets of
// an assignment are in p.lhs from base on, and are stored to from the
// last one back, as parse_assignment's recursion does.
func (p *parser) exprStat() ast.Stmt {
	fs := p.fs
	var v expDesc
	e, kind := p.primaryExp(&v)
	if kind == kindCall {
		if p.jit {
			fs.bc[v.info].setB(1) // no results
		}
		st := &ast.FuncCallStmt{Expr: e}
		st.SetLine(e.Line())
		return st
	}
	base := len(p.lhs)
	if p.jit || p.r51 {
		p.lhs = append(p.lhs, v)
	}
	lhs := []ast.Expr{e}
	for {
		if kind != kindVar {
			p.ls.syntaxError("syntax error")
		}
		if !p.testNext(',') {
			break
		}
		e, kind = p.primaryExp(&v)
		nvars := len(lhs)
		if p.jit {
			p.lhs = append(p.lhs, v)
			if v.k == vLocal {
				p.assignHazard(base, &v)
			}
			if p.level+nvars >= maxLevels {
				p.errorLimit(p.fs, maxLevels, "variable names")
			}
		} else {
			if p.r51 {
				p.lhs = append(p.lhs, v)
				if v.k == vLocal {
					p.rCheckConflict(base, &v)
				}
			}
			if limit := maxLevels - p.level; nvars > limit {
				p.errorLimit(p.fs, limit, "variables in assignment")
			}
		}
		lhs = append(lhs, e)
	}
	p.checkNext('=')
	var ev expDesc
	rhs := p.exprList(&ev)
	if p.jit {
		p.assignStore(base, len(rhs), &ev)
	} else if p.r51 {
		p.rAssignStore(base, len(rhs), &ev)
	}
	p.lhs = p.lhs[:base]
	st := &ast.AssignStmt{Lhs: lhs, Rhs: rhs}
	st.SetLine(lhs[0].Line())
	return st
}

// assignStore stores the values to the targets p.lhs[base:], the last
// value in e (parse_assignment).
func (p *parser) assignStore(base, nexps int, e *expDesc) {
	fs := p.fs
	last := len(p.lhs) - 1
	nvars := last - base + 1
	stored := false
	if nexps == nvars {
		if e.k == vCall {
			if fs.bc[e.info].op() == bcVARG { // a vararg assignment
				fs.freereg--
				e.k = vRelocable
			} else { // the base of a call is not relocable
				e.info = e.aux
				e.k = vNonReloc
			}
		}
		fs.store(p, &p.lhs[last], e)
		stored = true
	} else {
		p.assignAdjust(nvars, nexps, e)
	}
	for i := last; i >= base; i-- {
		if i == last && stored {
			continue
		}
		e.init(vNonReloc, fs.freereg-1)
		fs.store(p, &p.lhs[i], e)
	}
}

// assignHazard renames the tables and keys of the indexed targets before
// the local v, which an assignment to v overwrites before they are used
// (assign_hazard).
func (p *parser) assignHazard(base int, v *expDesc) {
	fs := p.fs
	reg := v.info
	tmp := fs.freereg
	hazard := false
	for i := len(p.lhs) - 2; i >= base; i-- {
		lh := &p.lhs[i]
		if lh.k != vIndexed {
			continue
		}
		if lh.info == reg { // t[i], t = 1, 2
			hazard = true
			lh.info = tmp
		}
		if lh.aux == reg { // t[i], i = 1, 2
			hazard = true
			lh.aux = tmp
		}
	}
	if hazard {
		fs.emit(p, insAD(bcMOV, tmp, reg))
		fs.regReserve(p, 1)
	}
}

/* functions */

// body parses a function's parameters and body; line is its linedefined.
// Under LuaJIT, e is the closure.
func (p *parser) body(e *expDesc, needself bool, line int) *ast.FunctionExpr {
	ls := p.ls
	pfs := p.fs
	p.openFunc(line, false)
	fs := p.fs
	parenLine := ls.t.line
	p.checkNext('(')
	if needself {
		p.newLocalVar("self", 0)
		p.adjustLocalVars(1)
	}
	parlist := &ast.ParList{Names: []string{}}
	if ls.t.typ != ')' {
		for {
			switch {
			case p.isName():
				name, _ := p.strCheckName()
				p.newLocalVar(name, len(parlist.Names))
				parlist.Names = append(parlist.Names, name)
			case ls.t.typ == tkDots:
				p.next()
				if !p.jit { // LUA_COMPAT_VARARG: a local "arg"
					p.newLocalVar("arg", len(parlist.Names))
				}
				parlist.HasVargs = true
				parlist.NoArg = p.jit
				fs.vararg = true
				fs.flags |= protoVararg
			default:
				ls.syntaxError("<name> or '...' expected")
			}
			if parlist.HasVargs || !p.testNext(',') {
				break
			}
		}
	}
	n := len(parlist.Names)
	if parlist.HasVargs && !p.jit {
		n++
	}
	p.adjustLocalVars(n)
	if p.jit {
		fs.numparams = fs.nactvar()
		fs.regReserve(p, fs.numparams)
	} else if p.r51 {
		fs.rReserve(p, fs.nactvar())
	}
	p.checkNext(')')
	if p.jit {
		fs.emit(p, insAD(bcFUNCF, 0, 0)) // the header's place
	}
	stmts := p.chunk()
	endLine := ls.t.line
	if !p.jit {
		p.checkMatch(tkEnd, tkFunction, line)
		p.closeFunc()
		if p.r51 { // pushclosure
			e.init(vRelocable, 0)
			e.aux = opOther51
		}
	} else {
		// fs_finish runs at the 'end', which is skipped after the closure
		if ls.t.typ != tkEnd {
			p.checkMatch(tkEnd, tkFunction, line)
		}
		ls.lastline = ls.linenumber // fs_finish(ls, (ls->lastline = ls->linenumber))
		p.closeFunc()
		e.init(vRelocable, pfs.emit(p, insAD(bcFNEW, 0, pfs.constGC())))
		if pfs.flags&protoChild == 0 {
			if pfs.flags&protoHasReturn != 0 {
				pfs.flags |= protoFixupReturn
			}
			pfs.flags |= protoChild
		}
		p.next()
	}
	fn := &ast.FunctionExpr{ParList: parlist, Stmts: stmts}
	fn.SetLine(parenLine)
	fn.SetLastLine(endLine)
	return fn
}

// funcArgs parses the arguments of a call: the expressions, and the lines
// where they start and end. Under LuaJIT, it emits the call of e.
func (p *parser) funcArgs(e *expDesc) (args []ast.Expr, line, endLine int) {
	ls := p.ls
	var a expDesc
	where := ls.linenumber
	switch ls.t.typ {
	case '(':
		if where != ls.lastline {
			ls.syntaxError("ambiguous syntax (function call x new statement)")
		}
		line = ls.t.line
		p.next()
		if ls.t.typ == ')' {
			args = []ast.Expr{}
			a.k = vVoid
		} else {
			args = p.exprList(&a)
			if p.jit && a.k == vCall { // f(a, b, g()) or f(a, b, ...)
				p.fs.bc[a.info].setB(0) // all the results
			} else if p.r51 {
				p.fs.rSetReturns(p, &a) // luaK_setmultret
			}
		}
		endLine = ls.t.line
		p.checkMatch(')', '(', where)
	case '{':
		t := p.constructor(&a)
		args, line, endLine = []ast.Expr{t}, t.Line(), t.LastLine()
	case tkString:
		s := &ast.StringExpr{Value: ls.t.sem}
		s.SetLine(ls.t.line)
		if p.jit {
			a.init(vKStr, 0)
			a.sval = ls.t.sem
		} else if p.r51 {
			a.init(vKStr, p.fs.rStringK(ls.t.sem))
		}
		p.next()
		args, line, endLine = []ast.Expr{s}, s.Line(), s.Line()
	default:
		ls.syntaxError("function arguments expected")
	}
	if p.jit {
		fs := p.fs
		base := e.info // the call's base register
		var ins bcIns
		if a.k == vCall {
			ins = insABC(bcCALLM, base, 2, a.aux-base-1-fr2)
		} else {
			if a.k != vVoid {
				fs.toNextReg(p, &a)
			}
			ins = insABC(bcCALL, base, 2, fs.freereg-base-fr2)
		}
		e.init(vCall, fs.emit(p, ins))
		e.aux = base
		fs.freereg = base + 1 // one result by default
	} else if p.r51 {
		fs := p.fs
		base := e.info
		if !hasMultRet51(a.k) && a.k != vVoid {
			fs.rExp2NextReg(p, &a)
		}
		e.init(vCall, 0)
		e.aux = base
		fs.freereg = base + 1
	}
	return args, line, endLine
}

/* expressions */

func (p *parser) primaryExp(v *expDesc) (ast.Expr, int) {
	ls := p.ls
	fs := p.fs
	var e ast.Expr
	var kind int
	var kv expDesc // a key, declared out of the loop so that it does not escape
	switch {
	case ls.t.typ == '(':
		where := ls.linenumber
		parenLine := ls.t.line
		p.next()
		var k int
		e, k = p.expr(v)
		p.checkMatch(')', '(', where)
		if p.jit {
			fs.discharge(p, v)
		} else if p.r51 {
			fs.rDischargeVars(v)
		}
		if call, ok := e.(*ast.FuncCallExpr); ok && k == kindCall {
			call.AdjustRet = true
		} else {
			if ex, ok := e.(*ast.Comma3Expr); ok {
				ex.AdjustRet = true
			}
			e.SetLine(parenLine)
		}
		kind = kindParen
	case p.isName():
		name, line := p.strCheckName()
		p.singleVar(fs, name, v, true)
		if p.r51 && v.k == vGlobal {
			v.info = fs.rStringK(name)
		}
		e = &ast.IdentExpr{Value: name}
		e.SetLine(line)
		kind = kindVar
	default:
		ls.syntaxError("unexpected symbol")
	}
	for {
		switch ls.t.typ {
		case '.':
			name, line := p.field(v)
			key := &ast.StringExpr{Value: name}
			key.SetLine(line)
			a := &ast.AttrGetExpr{Object: e, Key: key}
			a.SetLine(e.Line())
			e, kind = a, kindVar
		case '[':
			if p.jit {
				fs.toAnyReg(p, v)
			} else if p.r51 {
				fs.rExp2AnyReg(p, v)
			}
			key := p.bracket(&kv)
			if p.jit {
				fs.index(p, v, &kv)
			} else if p.r51 {
				fs.rIndexed(p, v, &kv)
			}
			a := &ast.AttrGetExpr{Object: e, Key: key}
			a.SetLine(e.Line())
			e, kind = a, kindVar
		case ':':
			p.next()
			method, _ := p.strCheckName()
			if p.jit {
				fs.method(p, v, method)
			} else if p.r51 {
				fs.rSelf(p, v, method)
			}
			args, line, endLine := p.funcArgs(v)
			c := &ast.FuncCallExpr{Method: method, Receiver: e, Args: args, ArgsLine: line, ArgsEndLine: endLine}
			c.SetLine(e.Line())
			e, kind = c, kindCall
		case '(', tkString, '{':
			if p.jit {
				fs.toNextReg(p, v)
				fs.regReserve(p, fr2)
			} else if p.r51 {
				fs.rExp2NextReg(p, v)
			}
			args, line, endLine := p.funcArgs(v)
			c := &ast.FuncCallExpr{Func: e, Args: args, ArgsLine: line, ArgsEndLine: endLine}
			c.SetLine(e.Line())
			e, kind = c, kindCall
		default:
			return e, kind
		}
	}
}

// bracket parses '[' expr ']' (expr_bracket).
func (p *parser) bracket(v *expDesc) ast.Expr {
	p.next()
	e, _ := p.expr(v)
	if p.jit {
		p.fs.toVal(p, v)
	} else if p.r51 {
		p.fs.rExp2Val(p, v)
	}
	p.checkNext(']')
	return e
}

func (p *parser) simpleExp(v *expDesc) (ast.Expr, int) {
	ls := p.ls
	var e ast.Expr
	switch ls.t.typ {
	case tkNumber:
		if ls.t.cdata { // go-moonscript patch: LuaJIT's FFI numerals
			e = &ast.CdataNumberExpr{Value: ls.t.sem}
			v.init(vKCdata, 0)
		} else {
			e = &ast.NumberExpr{Value: ls.t.sem}
			v.init(vKNum, 0)
			v.nval = ls.t.num
			if p.r51 {
				v.nval = lua51Number(ls.t.sem)
			}
		}
	case tkString:
		e = &ast.StringExpr{Value: ls.t.sem}
		v.init(vKStr, 0)
		v.sval = ls.t.sem
		if p.r51 { // codestring
			v.info = p.fs.rStringK(ls.t.sem)
		}
	case tkNil:
		e = &ast.NilExpr{}
		v.init(vKNil, 0)
	case tkTrue:
		e = &ast.TrueExpr{}
		v.init(vKTrue, 0)
	case tkFalse:
		e = &ast.FalseExpr{}
		v.init(vKFalse, 0)
	case tkDots:
		fs := p.fs
		if !fs.vararg {
			ls.syntaxError("cannot use '...' outside a vararg function")
		}
		e = &ast.Comma3Expr{}
		if p.jit {
			fs.regReserve(p, 1)
			base := fs.freereg - 1
			v.init(vCall, fs.emit(p, insABC(bcVARG, base, 2, fs.numparams)))
			v.aux = base
		} else if p.r51 {
			v.init(vVararg, 0)
		}
	case '{':
		return p.constructor(v), kindOther
	case tkFunction:
		line := ls.t.line
		p.next()
		body := p.body(v, false, ls.linenumber)
		fn := &ast.FunctionExpr{ParList: body.ParList, Stmts: body.Stmts}
		fn.SetLine(line)
		fn.SetLastLine(body.LastLine())
		return fn, kindOther
	default:
		return p.primaryExp(v)
	}
	e.SetLine(ls.t.line)
	p.next()
	return e, kindOther
}

// tableCons is the state of a table constructor under LuaJIT
// (expr_table).
type tableCons struct {
	pc       uint32 // the TNEW or TDUP
	freg     uint32 // the register after the table
	template bool   // a template table holds the constant items
	vcall    bool   // the last item is a call or "..." in the array part
	narr     uint32 // the next array index
	// Lua 5.1's ConsControl (lua51regs.go): the last list item, the
	// number of those not stored yet
	v51     expDesc
	tostore int
}

func (p *parser) constructor(v *expDesc) *ast.TableExpr {
	ls := p.ls
	fs := p.fs
	where := ls.linenumber
	line, prevLine := ls.t.line, ls.prevLine
	if prevLine == 0 {
		prevLine = line
	}
	var c tableCons
	if p.jit {
		c = tableCons{freg: fs.freereg, narr: 1}
		c.pc = fs.emit(p, insAD(bcTNEW, c.freg, 0))
		v.init(vNonReloc, c.freg)
		fs.regReserve(p, 1)
		c.freg++
	} else if p.r51 {
		fs.rReserve(p, 1) // the NEWTABLE
		v.init(vNonReloc, fs.freereg-1)
		c.v51.init(vVoid, 0)
	}
	p.checkNext('{')
	fields := []*ast.Field{}
	// declared out of the loop, so that their address passed to the
	// expression functions does not make them escape
	var key, val expDesc
	for ls.t.typ != '}' {
		var k ast.Expr
		c.vcall = false
		if p.r51 {
			p.rCloseListField(&c, v)
		}
		reg := fs.freereg // recfield's
		switch {
		case ls.t.typ == '[':
			k = p.bracket(&key)
			if p.jit && !key.isK() {
				fs.index(p, v, &key)
			}
			p.checkNext('=')
			if p.r51 {
				fs.rExp2RK(p, &key)
			}
		case p.isName() && ls.lookahead() == '=':
			name, nameLine := p.strCheckName()
			s := &ast.StringExpr{Value: name}
			s.SetLine(nameLine)
			k = s
			key.init(vKStr, 0)
			key.sval = name
			if p.r51 {
				key.info = fs.rStringK(name)
			}
			p.checkNext('=')
			if p.r51 {
				fs.rExp2RK(p, &key)
			}
		default:
			key.init(vKNum, 0)
			key.nval = float64(c.narr)
			c.narr++
			c.vcall = true
		}
		value, _ := p.expr(&val)
		fields = append(fields, &ast.Field{Key: k, Value: value})
		if p.jit {
			p.tableItem(&c, v, &key, &val)
			fs.freereg = c.freg
		} else if p.r51 {
			if k != nil { // recfield
				fs.rExp2RK(p, &val)
				fs.freereg = reg
			} else { // listfield
				c.v51 = val
				c.tostore++
			}
		}
		if !p.testNext(',') && !p.testNext(';') {
			break
		}
	}
	endLine := ls.t.line
	p.checkMatch('}', '{', where)
	if p.jit {
		p.tableEnd(&c, v)
	} else if p.r51 {
		p.rLastListField(&c, v)
	}
	t := &ast.TableExpr{Fields: fields, PrevLine: prevLine}
	t.SetLine(line)
	t.SetLastLine(endLine)
	return t
}

// tableItem stores an item of the table constructor t, or keeps it in the
// template table if the key is constant and the value too, or the key is
// a string.
func (p *parser) tableItem(c *tableCons, t, key, val *expDesc) {
	fs := p.fs
	if key.isK() && key.k != vKNil && (key.k == vKStr || val.isKNoJump()) {
		if !c.template { // the template table, made on demand
			c.template = true
			fs.bc[c.pc] = insAD(bcTDUP, c.freg-1, fs.constGC())
		}
		c.vcall = false
		if val.isKNoJump() { // in the template table
			return
		}
	}
	if val.k != vCall {
		fs.toAnyReg(p, val)
		c.vcall = false
	}
	if key.isK() {
		fs.index(p, t, key)
	}
	fs.store(p, t, val)
}

// tableEnd stores all the results of a call or "..." at the end of the
// array part of the table constructor t.
func (p *parser) tableEnd(c *tableCons, t *expDesc) {
	fs := p.fs
	if c.vcall {
		i := fs.pc - 1
		if c.narr > 256 { // drops the TSETV and replaces the load of its key
			fs.pc--
			i--
		}
		biased := math.Float64frombits(0x43300000<<32 | uint64(c.narr-1)) // a biased integer
		fs.bc[i] = insAD(bcTSETM, c.freg, fs.constNum(biased))
		fs.bc[i-1].setB(0)
	}
	if c.pc == fs.pc-1 { // relocable if possible
		t.info = c.pc
		fs.freereg--
		t.k = vRelocable
	} else {
		t.k = vNonReloc // index may have changed it
	}
}

type binOpr struct {
	left, right int
	op          string
	jit         jitBinOp
}

var binOprs = map[int]binOpr{
	'+': {6, 6, "+", binAdd}, '-': {6, 6, "-", binSub}, '*': {7, 7, "*", binMul}, '/': {7, 7, "/", binDiv},
	'%': {7, 7, "%", binMod}, '^': {10, 9, "^", binPow}, tkConcat: {5, 4, "..", binConcat},
	tkNe: {3, 3, "~=", binNe}, tkEq: {3, 3, "==", binEq}, '<': {3, 3, "<", binLt}, tkLe: {3, 3, "<=", binLe},
	'>': {3, 3, ">", binGt}, tkGe: {3, 3, ">=", binGe},
	tkAnd: {2, 2, "and", binAnd}, tkOr: {1, 1, "or", binOr},
}

const unaryPriority = 8

func (p *parser) expr(v *expDesc) (ast.Expr, int) {
	e, kind, _ := p.subExpr(v, 0)
	return e, kind
}

// subExpr parses (simpleexp | unop subexpr) { binop subexpr } with binary
// operators of a priority higher than limit, and returns the operator
// after it.
func (p *parser) subExpr(v *expDesc, limit int) (ast.Expr, int, int) {
	ls := p.ls
	p.enterLevel()
	var e ast.Expr
	kind := kindOther
	switch ls.t.typ {
	case tkNot, '-', '#':
		op := ls.t.typ
		p.next()
		operand, _, _ := p.subExpr(v, unaryPriority)
		var bc bcOp
		switch op {
		case tkNot:
			e = &ast.UnaryNotOpExpr{Expr: operand}
			bc = bcNOT
		case '-':
			e = &ast.UnaryMinusOpExpr{Expr: operand}
			bc = bcUNM
		default:
			e = &ast.UnaryLenOpExpr{Expr: operand}
			bc = bcLEN
		}
		if p.jit {
			p.fs.unop(p, bc, v)
		} else if p.r51 {
			p.fs.rPrefix(p, op, v)
		}
		e.SetLine(operand.Line())
	default:
		e, kind = p.simpleExp(v)
	}
	op := ls.t.typ
	var v2 expDesc // out of the loop, so that it does not escape
	for {
		b, ok := binOprs[op]
		if !ok || b.left <= limit {
			break
		}
		p.next()
		if p.jit {
			p.fs.binopLeft(p, b.jit, v)
		} else if p.r51 {
			p.fs.rInfix(p, binOps51[op], v)
		}
		line := e.Line()
		rhs, _, nextop := p.subExpr(&v2, b.right)
		if p.jit {
			p.fs.binop(p, b.jit, v, &v2)
		} else if p.r51 {
			p.fs.rPosfix(p, binOps51[op], v, &v2)
		}
		switch op {
		case tkOr, tkAnd:
			e = &ast.LogicalOpExpr{Lhs: e, Operator: b.op, Rhs: rhs}
		case tkConcat:
			e = &ast.StringConcatOpExpr{Lhs: e, Rhs: rhs}
		case '+', '-', '*', '/', '%', '^':
			e = &ast.ArithmeticOpExpr{Lhs: e, Operator: b.op, Rhs: rhs}
		default:
			e = &ast.RelationalOpExpr{Lhs: e, Operator: b.op, Rhs: rhs}
		}
		e.SetLine(line)
		kind = kindOther
		op = nextop
	}
	p.leaveLevel()
	return e, kind, op
}

// exprList parses expressions separated by commas, the last one in v
// (explist1 / expr_list).
func (p *parser) exprList(v *expDesc) []ast.Expr {
	e, _ := p.expr(v)
	list := []ast.Expr{e}
	for p.testNext(',') {
		if p.jit {
			p.fs.toNextReg(p, v)
		} else if p.r51 {
			p.fs.rExp2NextReg(p, v)
		}
		e, _ = p.expr(v)
		list = append(list, e)
	}
	return list
}

// Dump {{{

func isInlineDumpNode(rv reflect.Value) bool {
	switch rv.Kind() {
	case reflect.Struct, reflect.Slice, reflect.Interface, reflect.Ptr:
		return false
	default:
		return true
	}
}

func dump(node interface{}, level int, s string) string {
	rt := reflect.TypeOf(node)
	if fmt.Sprint(rt) == "<nil>" {
		return strings.Repeat(s, level) + "<nil>"
	}

	rv := reflect.ValueOf(node)
	buf := []string{}
	switch rt.Kind() {
	case reflect.Slice:
		if rv.Len() == 0 {
			return strings.Repeat(s, level) + "<empty>"
		}
		for i := 0; i < rv.Len(); i++ {
			buf = append(buf, dump(rv.Index(i).Interface(), level, s))
		}
	case reflect.Ptr:
		vt := rv.Elem()
		tt := rt.Elem()
		indicies := []int{}
		for i := 0; i < tt.NumField(); i++ {
			if strings.Index(tt.Field(i).Name, "Base") > -1 {
				continue
			}
			indicies = append(indicies, i)
		}
		switch {
		case len(indicies) == 0:
			return strings.Repeat(s, level) + "<empty>"
		case len(indicies) == 1 && isInlineDumpNode(vt.Field(indicies[0])):
			for _, i := range indicies {
				buf = append(buf, strings.Repeat(s, level)+"- Node$"+tt.Name()+": "+dump(vt.Field(i).Interface(), 0, s))
			}
		default:
			buf = append(buf, strings.Repeat(s, level)+"- Node$"+tt.Name())
			for _, i := range indicies {
				if isInlineDumpNode(vt.Field(i)) {
					inf := dump(vt.Field(i).Interface(), 0, s)
					buf = append(buf, strings.Repeat(s, level+1)+tt.Field(i).Name+": "+inf)
				} else {
					buf = append(buf, strings.Repeat(s, level+1)+tt.Field(i).Name+": ")
					buf = append(buf, dump(vt.Field(i).Interface(), level+2, s))
				}
			}
		}
	default:
		buf = append(buf, strings.Repeat(s, level)+fmt.Sprint(node))
	}
	return strings.Join(buf, "\n")
}

func Dump(chunk []ast.Stmt) string {
	return dump(chunk, 0, "   ")
}

// }}
