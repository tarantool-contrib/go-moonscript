package parse

// go-moonscript patch: the parser is Lua 5.1's lparser.c (Lua51) or LuaJIT
// 2.1's lj_parse.c (LuaJIT), recursive descent, ported to Go for what they
// accept and the errors they give (the code generation is compile.go's).
// It builds the syntax tree gopher-lua's yacc grammar built, with the same
// lines. Like luac, it checks the limits of LUAI_MAXCCALLS (200 syntax
// levels), LUAI_MAXVARS (200 local variables per function) and
// LUAI_MAXUPVALUES (60 upvalues per function), which are also LuaJIT's.

import (
	"fmt"
	"io"
	"reflect"
	"strings"

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
	defer func() {
		if e := recover(); e != nil {
			pe, ok := e.(*Error)
			if !ok {
				panic(e)
			}
			chunk, err = nil, pe
		}
	}()
	p := &parser{ls: newLexer(src, name, dialect), jit: dialect == LuaJIT}
	if !p.jit {
		p.level = lua51BaseLevel
	}
	p.openFunc(0, true)
	p.ls.nextToken()
	chunk = p.chunk()
	p.check(tkEOS)
	p.closeFunc()
	return chunk, nil
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
}

// gola is a goto or label of LuaJIT's (lj_parse.c's VarInfo): the number
// of active local variables where it is, and the line of a goto.
type gola struct {
	name    string
	isLabel bool
	slot    int
	line    int
	valid   bool
}

// breakName is a break's name as a goto (no Lua name has a NUL).
const breakName = "\x00break"

type funcState struct {
	prev    *funcState
	line    int // linedefined, 0 for the main function
	vararg  bool
	actvar  []varInfo // the active local variables
	pending []varInfo // declared, not active yet
	upvals  []int     // variable ids
	bl      *blockScope
	vstack  []gola // LuaJIT
}

type parser struct {
	ls     *lexer
	fs     *funcState
	jit    bool
	level  int
	nextID int
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
	fs := &funcState{prev: p.fs, line: line, vararg: vararg}
	p.fs = fs
	p.enterBlock(false)
}

func (p *parser) closeFunc() {
	p.leaveBlock()
	p.fs = p.fs.prev
}

func (p *parser) enterBlock(isLoop bool) {
	fs := p.fs
	fs.bl = &blockScope{prev: fs.bl, nactvar: len(fs.actvar), isLoop: isLoop, vstart: len(fs.vstack)}
}

// leaveBlock is fscope_end: under LuaJIT, it resolves the breaks of a
// loop, and the gotos and labels of the block, or passes them on.
func (p *parser) leaveBlock() {
	fs := p.fs
	bl := fs.bl
	fs.actvar = fs.actvar[:bl.nactvar]
	fs.bl = bl.prev
	if bl.hasBreak {
		if bl.isLoop {
			idx := p.golaNew(breakName, true, 0)
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

/* LuaJIT's gotos and labels (they are compiled by compile.go) */

func (p *parser) golaNew(name string, isLabel bool, line int) int {
	fs := p.fs
	fs.vstack = append(fs.vstack, gola{name: name, isLabel: isLabel, slot: len(fs.actvar), line: line, valid: true})
	return len(fs.vstack) - 1
}

// golaFindLabel reports whether the label is in the current block.
func (p *parser) golaFindLabel(name string) bool {
	fs := p.fs
	for _, v := range fs.vstack[fs.bl.vstart:] {
		if v.valid && v.isLabel && v.name == name {
			return true
		}
	}
	return false
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
			vg.valid = false
		}
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
					vg.valid = false
				}
			}
		} else if bl.prev != nil {
			if v.name == breakName {
				bl.prev.hasBreak = true
			} else {
				bl.prev.hasGola = true
			}
			v.slot = bl.nactvar
		} else if v.name == breakName {
			p.ls.errorAt(v.line, "no loop to break", 0)
		} else {
			p.ls.errorAt(v.line, fmt.Sprintf("undefined label '%s'", v.name), 0)
		}
	}
}

// newLocalVar declares the n-th variable of a declaration.
func (p *parser) newLocalVar(name string, n int) {
	fs := p.fs
	if len(fs.actvar)+n+1 > maxVars {
		p.errorLimit(fs, maxVars, "local variables")
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
// referenced from, as singlevaraux does.
func (p *parser) singleVar(fs *funcState, name string) (int, bool) {
	if fs == nil {
		return 0, false // a global
	}
	for i := len(fs.actvar) - 1; i >= 0; i-- {
		if fs.actvar[i].name == name {
			return fs.actvar[i].id, true
		}
	}
	id, ok := p.singleVar(fs.prev, name)
	if !ok {
		return 0, false
	}
	for _, u := range fs.upvals {
		if u == id {
			return id, true
		}
	}
	if len(fs.upvals)+1 > maxUpvalues {
		p.errorLimit(fs, maxUpvalues, "upvalues")
	}
	fs.upvals = append(fs.upvals, id)
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

// statement parses a statement into stmts; it reports whether the
// statement must be the last one of its block.
func (p *parser) statement(stmts *[]ast.Stmt) bool {
	ls := p.ls
	line := ls.linenumber
	tokLine := ls.t.line
	add := func(s ast.Stmt) { *stmts = append(*stmts, s) }
	switch ls.t.typ {
	case tkIf:
		add(p.ifStat(line))
	case tkWhile:
		p.next()
		cond, _ := p.expr()
		p.enterBlock(true)
		p.checkNext(tkDo)
		body := p.block()
		endLine := ls.t.line
		p.checkMatch(tkEnd, tkWhile, line)
		p.leaveBlock()
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
		p.enterBlock(true)  // the loop
		p.enterBlock(false) // the scope
		p.next()
		body := p.chunk()
		p.checkMatch(tkUntil, tkRepeat, line)
		cond, _ := p.expr() // in the scope
		p.leaveBlock()
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
			p.adjustLocalVars(1)
			fn := p.body(false, ls.linenumber)
			st := &ast.LocalAssignStmt{Names: []string{name}, Exprs: []ast.Expr{fn}, IsFunction: true}
			st.SetLine(tokLine)
			st.SetLastLine(fn.LastLine())
			add(st)
		} else {
			add(p.localStat(tokLine))
		}
	case tkReturn:
		p.next()
		var exprs []ast.Expr
		if !blockFollow(ls.t.typ) && ls.t.typ != ';' {
			exprs = p.exprList()
		}
		st := &ast.ReturnStmt{Exprs: exprs}
		st.SetLine(tokLine)
		add(st)
		return true
	case tkBreak:
		p.next()
		if p.jit { // a goto, resolved when the loop ends
			p.fs.bl.hasBreak = true
			p.golaNew(breakName, false, ls.lastline)
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
			p.fs.bl.hasGola = true
			p.golaNew(name, false, ls.lastline)
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

func (p *parser) ifStat(line int) ast.Stmt {
	ls := p.ls
	ifLine := ls.t.line
	p.next()
	cond, _ := p.expr()
	p.checkNext(tkThen)
	top := &ast.IfStmt{Condition: cond, Then: p.block()}
	top.SetLine(ifLine)
	cur := top
	for ls.t.typ == tkElseif {
		elseifLine := ls.t.line
		p.next()
		cond, _ := p.expr()
		p.checkNext(tkThen)
		st := &ast.IfStmt{Condition: cond, Then: p.block()}
		st.SetLine(elseifLine)
		cur.Else = []ast.Stmt{st}
		cur = st
	}
	if p.testNext(tkElse) {
		cur.Else = p.block()
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
		p.newLocalVar("(for index)", 0)
		p.newLocalVar("(for limit)", 1)
		p.newLocalVar("(for step)", 2)
		p.newLocalVar(name, 3)
		p.next()
		init, _ := p.expr()
		p.checkNext(',')
		limit, _ := p.expr()
		var step ast.Expr
		if p.testNext(',') {
			step, _ = p.expr()
		}
		st = &ast.NumberForStmt{Name: name, Init: init, Limit: limit, Step: step, Stmts: p.forBody(1)}
	case ',', tkIn:
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
		exprs := p.exprList()
		st = &ast.GenericForStmt{Names: names, Exprs: exprs, Stmts: p.forBody(len(names))}
	default:
		ls.syntaxError("'=' or 'in' expected")
	}
	st.SetLine(forLine)
	st.SetLastLine(ls.t.line)
	p.checkMatch(tkEnd, tkFor, line)
	p.leaveBlock()
	return st
}

func (p *parser) forBody(nvars int) []ast.Stmt {
	p.adjustLocalVars(3) // the control variables
	p.checkNext(tkDo)
	p.enterBlock(false)
	p.adjustLocalVars(nvars)
	stmts := p.block()
	p.leaveBlock()
	return stmts
}

func (p *parser) funcStat(line int) ast.Stmt {
	ls := p.ls
	p.next()
	// funcname: NAME {'.' NAME} [':' NAME]
	name, nameLine := p.strCheckName()
	p.singleVar(p.fs, name)
	var fn ast.Expr = &ast.IdentExpr{Value: name}
	fn.SetLine(nameLine)
	for ls.t.typ == '.' {
		p.next()
		key, keyLine := p.strCheckName()
		k := &ast.StringExpr{Value: key}
		k.SetLine(keyLine)
		fn = &ast.AttrGetExpr{Object: fn, Key: k}
		fn.SetLine(keyLine)
	}
	funcname := &ast.FuncName{Func: fn}
	needself := false
	if ls.t.typ == ':' {
		needself = true
		p.next()
		method, _ := p.strCheckName()
		funcname = &ast.FuncName{Receiver: fn, Method: method}
	}
	body := p.body(needself, line)
	st := &ast.FuncDefStmt{Name: funcname, Func: body}
	st.SetLine(line)
	st.SetLastLine(body.LastLine())
	return st
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
	if p.testNext('=') {
		exprs = p.exprList()
	}
	p.adjustLocalVars(len(names))
	st := &ast.LocalAssignStmt{Names: names, Exprs: exprs}
	st.SetLine(line)
	return st
}

// labelStat parses LuaJIT's '::' NAME '::' and the labels right after it.
func (p *parser) labelStat(stmts *[]ast.Stmt) {
	ls := p.ls
	line := ls.t.line
	fs := p.fs
	fs.bl.hasGola = true
	p.next()
	name, _ := p.strCheckName()
	if p.golaFindLabel(name) {
		ls.lexError(fmt.Sprintf("duplicate label '%s'", name), 0)
	}
	idx := p.golaNew(name, true, 0)
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

func (p *parser) exprStat() ast.Stmt {
	e, kind := p.primaryExp()
	if kind == kindCall {
		st := &ast.FuncCallStmt{Expr: e}
		st.SetLine(e.Line())
		return st
	}
	lhs := []ast.Expr{e}
	for {
		if kind != kindVar {
			p.ls.syntaxError("syntax error")
		}
		if !p.testNext(',') {
			break
		}
		e, kind = p.primaryExp()
		nvars := len(lhs)
		if p.jit {
			if p.level+nvars >= maxLevels {
				p.errorLimit(p.fs, maxLevels, "variable names")
			}
		} else if limit := maxLevels - p.level; nvars > limit {
			p.errorLimit(p.fs, limit, "variables in assignment")
		}
		lhs = append(lhs, e)
	}
	p.checkNext('=')
	st := &ast.AssignStmt{Lhs: lhs, Rhs: p.exprList()}
	st.SetLine(lhs[0].Line())
	return st
}

/* functions */

// body parses a function's parameters and body; line is its linedefined.
func (p *parser) body(needself bool, line int) *ast.FunctionExpr {
	ls := p.ls
	p.openFunc(line, false)
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
				p.fs.vararg = true
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
	p.checkNext(')')
	stmts := p.chunk()
	endLine := ls.t.line
	p.checkMatch(tkEnd, tkFunction, line)
	p.closeFunc()
	fn := &ast.FunctionExpr{ParList: parlist, Stmts: stmts}
	fn.SetLine(parenLine)
	fn.SetLastLine(endLine)
	return fn
}

// funcArgs parses the arguments of a call: the expressions, and the lines
// where they start and end.
func (p *parser) funcArgs() (args []ast.Expr, line, endLine int) {
	ls := p.ls
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
		} else {
			args = p.exprList()
		}
		endLine = ls.t.line
		p.checkMatch(')', '(', where)
	case '{':
		t := p.constructor()
		args, line, endLine = []ast.Expr{t}, t.Line(), t.LastLine()
	case tkString:
		s := &ast.StringExpr{Value: ls.t.sem}
		s.SetLine(ls.t.line)
		p.next()
		args, line, endLine = []ast.Expr{s}, s.Line(), s.Line()
	default:
		ls.syntaxError("function arguments expected")
	}
	return args, line, endLine
}

/* expressions */

func (p *parser) primaryExp() (ast.Expr, int) {
	ls := p.ls
	var e ast.Expr
	var kind int
	switch {
	case ls.t.typ == '(':
		where := ls.linenumber
		parenLine := ls.t.line
		p.next()
		var k int
		e, k = p.expr()
		p.checkMatch(')', '(', where)
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
		p.singleVar(p.fs, name)
		e = &ast.IdentExpr{Value: name}
		e.SetLine(line)
		kind = kindVar
	default:
		ls.syntaxError("unexpected symbol")
	}
	for {
		switch ls.t.typ {
		case '.':
			p.next()
			name, line := p.strCheckName()
			key := &ast.StringExpr{Value: name}
			key.SetLine(line)
			a := &ast.AttrGetExpr{Object: e, Key: key}
			a.SetLine(e.Line())
			e, kind = a, kindVar
		case '[':
			p.next()
			key, _ := p.expr()
			p.checkNext(']')
			a := &ast.AttrGetExpr{Object: e, Key: key}
			a.SetLine(e.Line())
			e, kind = a, kindVar
		case ':':
			p.next()
			method, _ := p.strCheckName()
			args, line, endLine := p.funcArgs()
			c := &ast.FuncCallExpr{Method: method, Receiver: e, Args: args, ArgsLine: line, ArgsEndLine: endLine}
			c.SetLine(e.Line())
			e, kind = c, kindCall
		case '(', tkString, '{':
			args, line, endLine := p.funcArgs()
			c := &ast.FuncCallExpr{Func: e, Args: args, ArgsLine: line, ArgsEndLine: endLine}
			c.SetLine(e.Line())
			e, kind = c, kindCall
		default:
			return e, kind
		}
	}
}

func (p *parser) simpleExp() (ast.Expr, int) {
	ls := p.ls
	var e ast.Expr
	switch ls.t.typ {
	case tkNumber:
		if ls.t.cdata { // go-moonscript patch: LuaJIT's FFI numerals
			e = &ast.CdataNumberExpr{Value: ls.t.sem}
		} else {
			e = &ast.NumberExpr{Value: ls.t.sem}
		}
	case tkString:
		e = &ast.StringExpr{Value: ls.t.sem}
	case tkNil:
		e = &ast.NilExpr{}
	case tkTrue:
		e = &ast.TrueExpr{}
	case tkFalse:
		e = &ast.FalseExpr{}
	case tkDots:
		if !p.fs.vararg {
			ls.syntaxError("cannot use '...' outside a vararg function")
		}
		e = &ast.Comma3Expr{}
	case '{':
		return p.constructor(), kindOther
	case tkFunction:
		line := ls.t.line
		p.next()
		body := p.body(false, ls.linenumber)
		fn := &ast.FunctionExpr{ParList: body.ParList, Stmts: body.Stmts}
		fn.SetLine(line)
		fn.SetLastLine(body.LastLine())
		return fn, kindOther
	default:
		return p.primaryExp()
	}
	e.SetLine(ls.t.line)
	p.next()
	return e, kindOther
}

func (p *parser) constructor() *ast.TableExpr {
	ls := p.ls
	where := ls.linenumber
	line, prevLine := ls.t.line, ls.prevLine
	if prevLine == 0 {
		prevLine = line
	}
	p.checkNext('{')
	fields := []*ast.Field{}
	for ls.t.typ != '}' {
		switch {
		case p.isName():
			if ls.lookahead() != '=' {
				v, _ := p.expr()
				fields = append(fields, &ast.Field{Value: v})
				break
			}
			name, nameLine := p.strCheckName()
			key := &ast.StringExpr{Value: name}
			key.SetLine(nameLine)
			p.checkNext('=')
			v, _ := p.expr()
			fields = append(fields, &ast.Field{Key: key, Value: v})
		case ls.t.typ == '[':
			p.next()
			key, _ := p.expr()
			p.checkNext(']')
			p.checkNext('=')
			v, _ := p.expr()
			fields = append(fields, &ast.Field{Key: key, Value: v})
		default:
			v, _ := p.expr()
			fields = append(fields, &ast.Field{Value: v})
		}
		if !p.testNext(',') && !p.testNext(';') {
			break
		}
	}
	endLine := ls.t.line
	p.checkMatch('}', '{', where)
	t := &ast.TableExpr{Fields: fields, PrevLine: prevLine}
	t.SetLine(line)
	t.SetLastLine(endLine)
	return t
}

type binOpr struct {
	left, right int
	op          string
}

var binOprs = map[int]binOpr{
	'+': {6, 6, "+"}, '-': {6, 6, "-"}, '*': {7, 7, "*"}, '/': {7, 7, "/"}, '%': {7, 7, "%"},
	'^': {10, 9, "^"}, tkConcat: {5, 4, ".."},
	tkNe: {3, 3, "~="}, tkEq: {3, 3, "=="}, '<': {3, 3, "<"}, tkLe: {3, 3, "<="}, '>': {3, 3, ">"}, tkGe: {3, 3, ">="},
	tkAnd: {2, 2, "and"}, tkOr: {1, 1, "or"},
}

const unaryPriority = 8

func (p *parser) expr() (ast.Expr, int) {
	e, kind, _ := p.subExpr(0)
	return e, kind
}

// subExpr parses (simpleexp | unop subexpr) { binop subexpr } with binary
// operators of a priority higher than limit, and returns the operator
// after it.
func (p *parser) subExpr(limit int) (ast.Expr, int, int) {
	ls := p.ls
	p.enterLevel()
	var e ast.Expr
	kind := kindOther
	switch ls.t.typ {
	case tkNot, '-', '#':
		op := ls.t.typ
		p.next()
		operand, _, _ := p.subExpr(unaryPriority)
		switch op {
		case tkNot:
			e = &ast.UnaryNotOpExpr{Expr: operand}
		case '-':
			e = &ast.UnaryMinusOpExpr{Expr: operand}
		default:
			e = &ast.UnaryLenOpExpr{Expr: operand}
		}
		e.SetLine(operand.Line())
	default:
		e, kind = p.simpleExp()
	}
	op := ls.t.typ
	for {
		b, ok := binOprs[op]
		if !ok || b.left <= limit {
			break
		}
		p.next()
		line := e.Line()
		rhs, _, nextop := p.subExpr(b.right)
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

func (p *parser) exprList() []ast.Expr {
	e, _ := p.expr()
	list := []ast.Expr{e}
	for p.testNext(',') {
		e, _ = p.expr()
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
