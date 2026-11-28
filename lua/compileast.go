package lua

// go-moonscript patch: compiling a syntax tree that other code built or
// transformed (a bundler, a static analyser, a compiler to Lua), instead of
// printing Lua source and loading it again: CompileAST and LState.LoadAST.
//
// A tree from the parser (parse.ParseBytes) is what LoadChunk compiles, and
// LoadAST compiles it to the same function: the same bytecode, lines,
// locals and chunk name (when LoadChunk loads the source: it also has the
// checks of source loading below). Any tree is checked first (astCheck),
// for what the parser guarantees and the compiler relies on, so that a
// wrong tree is an error, a *CompileError at the node's line, rather than
// a crash of the compiler or of the VM:
//
//   - nodes: no nil statement, expression, field, parameter list or function
//     name where the compiler needs one (a nil pointer of a node type is
//     nil too), no node of an unknown type, the operators of the grammar,
//     numerals that convert to numbers, the lists the grammar never leaves
//     empty (the targets and values of an assignment, the names of a local
//     declaration and of a generic for, the expressions of a generic for),
//     a call statement holds a call, a call has a function or a receiver,
//     not both, assignment targets and function statement names are names
//     or indexings, lines are not negative;
//   - scopes: "..." only in a vararg function, break only in a loop;
//   - limits: 200 local variables and 60 upvalues per function (Lua 5.1's
//     LUAI_MAXVARS and LUAI_MAXUPVALUES, LuaJIT's LJ_MAX_LOCVAR and
//     LJ_MAX_UPVAL, with their messages), Lua 5.1's 32767 local variable
//     records per function, and the compiler's nesting limit
//     (maxCompileDepth, which also stops a tree with a cycle);
//   - dialect: goto statements, labels and LuaJIT's cdata numerals
//     (ast.CdataNumberExpr) only under DialectLuaJIT; under DialectLuaJIT no
//     function has the implicit local "arg" (ParList.NoArg needn't be set).
//
// The compiler checks the rest as it does for source: undefined and
// duplicate labels and gotos into the scope of a local (LuaJIT's messages),
// its register limit (256, what its instructions address: "function or
// expression too complex"), its limits of constants, closures and jumps
// ("control structure too long").
//
// Only source loading (LoadChunk, Load) has: the exact error messages and
// positions of lua5.1 and luajit for a chunk they reject, with the token
// near which they stop; the 200 levels of syntax nesting of their parsers
// (LUAI_MAXCCALLS, LJ_MAX_XLEVEL, counted over the parser's recursion, which
// a tree doesn't show), their limit of variables in an assignment; under
// DialectLua51 the model of lcode.c's register allocation
// (parse.Lua51Overflow: lua5.1's "function or expression too complex" at
// 250 registers, MAXSTACK), under DialectLuaJIT the model of LuaJIT's code
// generator (parse/jitcode.go: LJ_MAX_SLOTS, LJ_MAX_VSTACK, the limits of
// constants and jumps of LuaJIT's bytecode). Both models run in the parser,
// over the expression descriptors of its recursion; running them over a
// tree would take a second driver of the code generators (future work). A
// tree is limited by the compiler's limits, which keep its code within what
// the VM runs.
//
// Lines: a node's Line is where it starts, LastLine where it ends (0: on
// its Line), as the parser sets them. A tree built by other code may leave
// them 0: such a node is on the line of the node before it in source order
// (its parent, or the subtree before it: the line of the last token before
// it), and if its LastLine is 0 too, it ends on the last line of its
// subtree; a node before any line is on line 1, as the first token of a
// chunk is. So an unpositioned tree
// compiles as if its source were on one line, and a node inserted into a
// parsed tree takes the line of the code around it: error messages,
// debug.getinfo (currentline, linedefined, lastlinedefined) and tracebacks
// show lines of the tree. The tree is left as it is: the compiler works on
// a copy with the lines set. FuncCallExpr.ArgsLine and ArgsEndLine and
// TableExpr.PrevLine are 0 when unknown, as the compiler takes them.
// The spans of the nodes (ast.Span, ast.Expr.Parens) are not read: a tree
// compiles the same with them, without them (the zero Span) or with wrong
// ones, and the checker doesn't ask for them.
//
// Compiling never modifies the tree (go-moonscript patch: Compile stored a
// numeric for's default step and the folded operand of a unary minus in
// it), so a tree can be compiled again, from several goroutines, and kept.

import (
	"fmt"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
)

// The limits of local variables and upvalues per function, which both
// dialects have (LUAI_MAXVARS, LUAI_MAXUPVALUES; LJ_MAX_LOCVAR, LJ_MAX_UPVAL),
// and of Lua 5.1's local variable records per function (SHRT_MAX).
const (
	astMaxVars     = 200
	astMaxUpvalues = 60
	astMaxLocVars  = 32767
)

// CompileAST compiles the syntax tree of a chunk into the prototype of its
// main function, for the dialect, as LoadChunk(reader, chunkname) compiles
// the tree the parser builds from reader under that dialect. chunkname is a
// Lua chunk name, as LoadChunk's: "@" + a file name, "=" + a name shown as
// it is, or the source text of a string chunk.
//
// The tree is checked first (see compileast.go): a tree the compiler cannot
// compile is a *CompileError at the line of the node, "<chunk id>:<line>:
// <message>". The tree is not modified; lines a node lacks (0) are taken
// from the node before it.
//
// To compile a chunk with named parameters (gopher-lua's PR #402), prepend
// `local a, b = ...` (an ast.LocalAssignStmt of an ast.Comma3Expr): the
// main function is vararg, and its first locals are where parameters
// would be.
func CompileAST(chunk []ast.Stmt, chunkname string, dialect Dialect) (proto *FunctionProto, err error) {
	if dialect != DialectLua51 && dialect != DialectLuaJIT {
		return nil, fmt.Errorf("lua: unknown dialect %v", dialect)
	}
	name, parseName := ChunkID(chunkname, dialect), syntaxChunkID(chunkname, dialect)
	defer func() {
		// the checker should leave the compiler nothing to fail on; if it
		// does, an error rather than a crash of the caller
		if rcv := recover(); rcv != nil {
			if ce, ok := rcv.(*CompileError); ok {
				proto, err = nil, ce
				return
			}
			proto, err = nil, &CompileError{Message: fmt.Sprintf("internal compiler error: %v", rcv), source: parseName}
		}
	}()
	c := &astCheck{dialect: dialect, source: parseName, line: 1}
	c.chunk(chunk)
	if c.unlined {
		chunk = (&lineFiller{line: 1}).stmts(chunk)
	}
	proto, err = compileDialect(chunk, parseName, dialect)
	if err != nil {
		return nil, err
	}
	setSource(proto, name, chunkname)
	return proto, nil
}

// LoadAST loads the syntax tree of a chunk as LoadChunk loads source: it
// compiles it for the state's dialect (CompileAST), into a function of the
// state's current environment. chunkname is a Lua chunk name, as
// LoadChunk's. An error is an *ApiError of type ApiErrorSyntax, its Cause
// the *CompileError, as LoadChunk's.
func (ls *LState) LoadAST(chunk []ast.Stmt, chunkname string) (*LFunction, error) {
	proto, err := CompileAST(chunk, chunkname, ls.Dialect())
	if err != nil {
		return nil, newApiErrorE(ApiErrorSyntax, err)
	}
	return newLFunctionL(proto, ls.currentEnv(), 0), nil
}

/* the checker {{{ */

// astCheck checks a syntax tree for CompileAST, walking it in source order
// and resolving its names as the parser does (singlevar).
type astCheck struct {
	dialect Dialect
	source  string // the chunk's id in error messages
	line    int    // the line of the node being checked (lineFiller's)
	unlined bool   // a node has no line
	depth   int
	fs      *checkFunc
	nextID  int
}

// checkFunc is a function being checked: its active local variables (by
// id), the blocks they are declared in and its upvalues (the ids of the
// variables of enclosing functions it references).
type checkFunc struct {
	parent  *checkFunc
	line    int // linedefined, 0 for the main function
	vararg  bool
	actvar  []checkVar
	blocks  []checkBlock
	upvals  []int
	nlocvar int // the local variables declared (Lua 5.1's records)
}

type checkVar struct {
	name string
	id   int
}

type checkBlock struct {
	nactvar int
	loop    bool
}

func (c *astCheck) fail(format string, args ...any) {
	panic(&CompileError{Line: c.line, Message: fmt.Sprintf(format, args...), source: c.source})
}

// at takes the node's lines: its Line is the current line, if it has one.
func (c *astCheck) at(n ast.PositionHolder) {
	line, last := n.Line(), n.LastLine()
	if line < 0 || last < 0 {
		c.fail("invalid line %d", min(line, last))
	}
	if line == 0 {
		c.unlined = true
	} else {
		c.line = line
	}
}

func (c *astCheck) enter() {
	c.depth++
	if c.depth > maxCompileDepth {
		c.fail("chunk has too many syntax levels")
	}
}

func (c *astCheck) leave() { c.depth-- }

// nodeType is a node's type for messages: "FuncCallExpr" for an
// *ast.FuncCallExpr, the Go type otherwise.
func nodeType(n any) string {
	s := fmt.Sprintf("%T", n)
	if t, ok := strings.CutPrefix(s, "*ast."); ok {
		return t
	}
	return s
}

// knownNode reports whether n is a node of a type of package ast, and
// whether it is a nil pointer of one.
func knownNode(n ast.PositionHolder) (known, isNil bool) {
	switch n := n.(type) {
	case *ast.AssignStmt:
		return true, n == nil
	case *ast.LocalAssignStmt:
		return true, n == nil
	case *ast.FuncCallStmt:
		return true, n == nil
	case *ast.DoBlockStmt:
		return true, n == nil
	case *ast.WhileStmt:
		return true, n == nil
	case *ast.RepeatStmt:
		return true, n == nil
	case *ast.IfStmt:
		return true, n == nil
	case *ast.NumberForStmt:
		return true, n == nil
	case *ast.GenericForStmt:
		return true, n == nil
	case *ast.FuncDefStmt:
		return true, n == nil
	case *ast.ReturnStmt:
		return true, n == nil
	case *ast.BreakStmt:
		return true, n == nil
	case *ast.LabelStmt:
		return true, n == nil
	case *ast.GotoStmt:
		return true, n == nil
	case *ast.TrueExpr:
		return true, n == nil
	case *ast.FalseExpr:
		return true, n == nil
	case *ast.NilExpr:
		return true, n == nil
	case *ast.NumberExpr:
		return true, n == nil
	case *ast.StringExpr:
		return true, n == nil
	case *ast.CdataNumberExpr:
		return true, n == nil
	case *ast.Comma3Expr:
		return true, n == nil
	case *ast.IdentExpr:
		return true, n == nil
	case *ast.AttrGetExpr:
		return true, n == nil
	case *ast.TableExpr:
		return true, n == nil
	case *ast.FuncCallExpr:
		return true, n == nil
	case *ast.LogicalOpExpr:
		return true, n == nil
	case *ast.RelationalOpExpr:
		return true, n == nil
	case *ast.StringConcatOpExpr:
		return true, n == nil
	case *ast.ArithmeticOpExpr:
		return true, n == nil
	case *ast.UnaryMinusOpExpr:
		return true, n == nil
	case *ast.UnaryNotOpExpr:
		return true, n == nil
	case *ast.UnaryLenOpExpr:
		return true, n == nil
	case *ast.FunctionExpr:
		return true, n == nil
	}
	return false, false
}

// chunk checks the main function.
func (c *astCheck) chunk(chunk []ast.Stmt) {
	c.fs = &checkFunc{vararg: true}
	c.enterBlock(false)
	c.stmts(chunk)
	c.leaveBlock()
	c.fs = nil
}

func (c *astCheck) enterBlock(loop bool) {
	fs := c.fs
	fs.blocks = append(fs.blocks, checkBlock{nactvar: len(fs.actvar), loop: loop})
}

func (c *astCheck) leaveBlock() {
	fs := c.fs
	fs.actvar = fs.actvar[:fs.blocks[len(fs.blocks)-1].nactvar]
	fs.blocks = fs.blocks[:len(fs.blocks)-1]
}

// limit fails with Lua's message for a function with more than limit what.
func (c *astCheck) limit(fs *checkFunc, limit int, what string) {
	if fs.line == 0 {
		c.fail("main function has more than %d %s", limit, what)
	}
	c.fail("function at line %d has more than %d %s", fs.line, limit, what)
}

// declare checks the declaration of n local variables, which activate
// makes active; extra are declared with them but active already ("self").
func (c *astCheck) declare(n int) {
	fs := c.fs
	if len(fs.actvar)+n > astMaxVars {
		c.limit(fs, astMaxVars, "local variables")
	}
	if c.dialect == DialectLua51 {
		if fs.nlocvar+n > astMaxLocVars {
			c.fail("too many local variables")
		}
		fs.nlocvar += n
	}
}

func (c *astCheck) activate(names ...string) {
	fs := c.fs
	for _, name := range names {
		c.nextID++
		fs.actvar = append(fs.actvar, checkVar{name, c.nextID})
	}
}

// resolve resolves a name in fs, making it an upvalue of the functions
// between its declaration and fs (singlevaraux): the id of its variable,
// false for a global.
func (c *astCheck) resolve(fs *checkFunc, name string) (int, bool) {
	if fs == nil {
		return 0, false
	}
	for i := len(fs.actvar) - 1; i >= 0; i-- {
		if fs.actvar[i].name == name {
			return fs.actvar[i].id, true
		}
	}
	id, ok := c.resolve(fs.parent, name)
	if !ok {
		return 0, false
	}
	for _, u := range fs.upvals {
		if u == id {
			return id, true
		}
	}
	if len(fs.upvals)+1 > astMaxUpvalues {
		c.limit(fs, astMaxUpvalues, "upvalues")
	}
	fs.upvals = append(fs.upvals, id)
	return id, true
}

func (c *astCheck) block(stmts []ast.Stmt, loop bool) {
	c.enterBlock(loop)
	c.stmts(stmts)
	c.leaveBlock()
}

func (c *astCheck) stmts(stmts []ast.Stmt) {
	for _, s := range stmts {
		c.stmt(s)
	}
}

func (c *astCheck) stmt(s ast.Stmt) {
	if s == nil {
		c.fail("nil statement")
	}
	if known, isNil := knownNode(s); isNil {
		c.fail("nil %s", nodeType(s))
	} else if !known {
		c.fail("unknown statement type %s", nodeType(s))
	}
	c.at(s)
	c.enter()
	defer c.leave()
	switch st := s.(type) {
	case *ast.AssignStmt:
		if len(st.Lhs) == 0 {
			c.fail("assignment without targets")
		}
		if len(st.Rhs) == 0 {
			c.fail("assignment without values")
		}
		for _, e := range st.Lhs {
			c.target(e)
		}
		c.exprs(st.Rhs, "value")
	case *ast.LocalAssignStmt:
		if len(st.Names) == 0 {
			c.fail("local declaration without names")
		}
		c.declare(len(st.Names))
		if st.IsFunction && len(st.Names) == 1 && len(st.Exprs) == 1 {
			if _, ok := st.Exprs[0].(*ast.FunctionExpr); ok { // local function f
				c.activate(st.Names...)
				c.expr(st.Exprs[0], "value")
				return
			}
		}
		c.exprs(st.Exprs, "value")
		c.activate(st.Names...)
	case *ast.FuncCallStmt:
		call, ok := st.Expr.(*ast.FuncCallExpr)
		if !ok {
			c.fail("call statement without a call (%s)", nodeType(st.Expr))
		}
		c.expr(call, "call")
	case *ast.DoBlockStmt:
		c.block(st.Stmts, false)
	case *ast.WhileStmt:
		c.expr(st.Condition, "condition")
		c.block(st.Stmts, true)
	case *ast.RepeatStmt:
		c.enterBlock(true)
		c.stmts(st.Stmts)
		c.expr(st.Condition, "condition") // in the scope of the body
		c.leaveBlock()
	case *ast.IfStmt:
		// an elseif chain is a loop, as in the compiler (compileIfStmt)
		for {
			c.expr(st.Condition, "condition")
			c.block(st.Then, false)
			if len(st.Else) == 1 {
				if elseif, ok := st.Else[0].(*ast.IfStmt); ok && elseif != nil {
					st = elseif
					c.at(st)
					continue
				}
			}
			c.block(st.Else, false)
			break
		}
	case *ast.NumberForStmt:
		c.expr(st.Init, "initial value")
		c.expr(st.Limit, "limit")
		if st.Step != nil {
			c.expr(st.Step, "step")
		}
		c.enterBlock(true)
		c.declare(4)
		c.activate("(for index)", "(for limit)", "(for step)")
		c.enterBlock(false)
		c.activate(st.Name)
		c.block(st.Stmts, false)
		c.leaveBlock()
		c.leaveBlock()
	case *ast.GenericForStmt:
		if len(st.Names) == 0 {
			c.fail("for statement without names")
		}
		if len(st.Exprs) == 0 {
			c.fail("for statement without expressions")
		}
		c.enterBlock(true)
		c.declare(3 + len(st.Names))
		c.exprs(st.Exprs, "expression")
		c.activate("(for generator)", "(for state)", "(for control)")
		c.enterBlock(false)
		c.activate(st.Names...)
		c.block(st.Stmts, false)
		c.leaveBlock()
		c.leaveBlock()
	case *ast.FuncDefStmt:
		if st.Name == nil {
			c.fail("function statement without a name")
		}
		if st.Func == nil {
			c.fail("function statement without a function")
		}
		switch {
		case st.Name.Func != nil && st.Name.Receiver != nil:
			c.fail("function name with both a target and a receiver")
		case st.Name.Func != nil:
			c.target(st.Name.Func)
		default:
			c.expr(st.Name.Receiver, "receiver")
		}
		c.expr0(st.Func, st.Name.Func == nil)
	case *ast.ReturnStmt:
		c.exprs(st.Exprs, "value")
	case *ast.BreakStmt:
		blocks := c.fs.blocks
		i := len(blocks) - 1
		for i >= 0 && !blocks[i].loop {
			i--
		}
		if i < 0 {
			c.fail("no loop to break")
		}
	case *ast.LabelStmt:
		if c.dialect != DialectLuaJIT {
			c.fail("labels need DialectLuaJIT")
		}
	case *ast.GotoStmt:
		if c.dialect != DialectLuaJIT {
			c.fail("goto needs DialectLuaJIT")
		}
	}
}

// target checks an assignment target: a name or an indexing.
func (c *astCheck) target(e ast.Expr) {
	switch e.(type) {
	case *ast.IdentExpr, *ast.AttrGetExpr:
		c.expr(e, "target")
	case nil:
		c.fail("nil target")
	default:
		c.fail("cannot assign to %s", nodeType(e))
	}
}

func (c *astCheck) exprs(exprs []ast.Expr, what string) {
	for _, e := range exprs {
		c.expr(e, what)
	}
}

func (c *astCheck) expr(e ast.Expr, what string) {
	if e == nil {
		c.fail("nil %s", what)
	}
	c.expr0(e, false)
}

// expr0 checks an expression; needself declares a function's "self".
func (c *astCheck) expr0(e ast.Expr, needself bool) {
	if known, isNil := knownNode(e); isNil {
		c.fail("nil %s", nodeType(e))
	} else if !known {
		c.fail("unknown expression type %s", nodeType(e))
	}
	c.at(e)
	c.enter()
	defer c.leave()
	switch ex := e.(type) {
	case *ast.TrueExpr, *ast.FalseExpr, *ast.NilExpr, *ast.StringExpr:
	case *ast.NumberExpr:
		if _, err := parseNumber(ex.Value); err != nil {
			c.fail("malformed number '%s'", ex.Value)
		}
	case *ast.CdataNumberExpr:
		if c.dialect != DialectLuaJIT {
			c.fail("cdata numeral '%s' needs DialectLuaJIT", ex.Value)
		}
		if ex.Value == "" {
			c.fail("malformed number ''")
		}
	case *ast.Comma3Expr:
		if !c.fs.vararg {
			c.fail("cannot use '...' outside a vararg function")
		}
	case *ast.IdentExpr:
		c.resolve(c.fs, ex.Value)
	case *ast.AttrGetExpr:
		c.expr(ex.Object, "indexed value")
		c.expr(ex.Key, "key")
	case *ast.TableExpr:
		for _, f := range ex.Fields {
			if f == nil {
				c.fail("nil table field")
			}
			if f.Key != nil {
				c.expr(f.Key, "key")
			}
			c.expr(f.Value, "value")
		}
	case *ast.FuncCallExpr:
		switch {
		case ex.Func != nil && ex.Receiver != nil:
			c.fail("call with both a function and a receiver")
		case ex.Func != nil:
			c.expr(ex.Func, "function")
		case ex.Receiver != nil:
			c.expr(ex.Receiver, "receiver")
		default:
			c.fail("call without a function")
		}
		c.exprs(ex.Args, "argument")
	case *ast.LogicalOpExpr:
		if ex.Operator != "and" && ex.Operator != "or" {
			c.fail("unknown logical operator '%s'", ex.Operator)
		}
		c.expr(ex.Lhs, "operand")
		c.expr(ex.Rhs, "operand")
	case *ast.RelationalOpExpr:
		switch ex.Operator {
		case "==", "~=", "<", "<=", ">", ">=":
		default:
			c.fail("unknown relational operator '%s'", ex.Operator)
		}
		c.expr(ex.Lhs, "operand")
		c.expr(ex.Rhs, "operand")
	case *ast.ArithmeticOpExpr:
		switch ex.Operator {
		case "+", "-", "*", "/", "%", "^":
		default:
			c.fail("unknown arithmetic operator '%s'", ex.Operator)
		}
		c.expr(ex.Lhs, "operand")
		c.expr(ex.Rhs, "operand")
	case *ast.StringConcatOpExpr:
		c.expr(ex.Lhs, "operand")
		c.expr(ex.Rhs, "operand")
	case *ast.UnaryMinusOpExpr:
		c.expr(ex.Expr, "operand")
	case *ast.UnaryNotOpExpr:
		c.expr(ex.Expr, "operand")
	case *ast.UnaryLenOpExpr:
		c.expr(ex.Expr, "operand")
	case *ast.FunctionExpr:
		c.function(ex, needself)
	default:
		c.fail("%s is not an expression", nodeType(e))
	}
}

// function checks a function and its body, as a function of its own.
func (c *astCheck) function(fn *ast.FunctionExpr, needself bool) {
	pl := fn.ParList
	if pl == nil {
		c.fail("function without a parameter list")
	}
	fs := &checkFunc{parent: c.fs, line: c.line, vararg: pl.HasVargs}
	c.fs = fs
	c.enterBlock(false)
	if needself {
		c.declare(1)
		c.activate("self")
	}
	n := len(pl.Names)
	arg := pl.HasVargs && c.dialect == DialectLua51 && CompatVarArg && !pl.NoArg
	if arg {
		n++
	}
	c.declare(n)
	c.activate(pl.Names...)
	if arg {
		c.activate("arg")
	}
	c.stmts(fn.Stmts)
	c.leaveBlock()
	c.fs = fs.parent
}

/* }}} */

/* the lines of an unpositioned tree {{{ */

// lineFiller copies a tree, giving a node that has no line (0) the line of
// the node before it in source order, and if it has no last line either,
// the last line of its subtree (line is the current line, the line of the
// last node copied).
type lineFiller struct {
	line int
}

// pos sets the lines of the copy of a node before its children are
// copied; done, with what pos returned, after them.
func (f *lineFiller) pos(n ast.PositionHolder) bool {
	if l := n.Line(); l > 0 {
		f.line = l
		return false
	}
	n.SetLine(f.line)
	return true
}

func (f *lineFiller) done(n ast.PositionHolder, unlined bool) {
	if unlined && n.LastLine() == 0 && f.line > n.Line() {
		n.SetLastLine(f.line)
	}
}

func (f *lineFiller) stmts(stmts []ast.Stmt) []ast.Stmt {
	if stmts == nil {
		return nil
	}
	out := make([]ast.Stmt, len(stmts))
	for i, s := range stmts {
		out[i] = f.stmt(s)
	}
	return out
}

func (f *lineFiller) exprs(exprs []ast.Expr) []ast.Expr {
	if exprs == nil {
		return nil
	}
	out := make([]ast.Expr, len(exprs))
	for i, e := range exprs {
		out[i] = f.expr(e)
	}
	return out
}

func (f *lineFiller) stmt(s ast.Stmt) ast.Stmt {
	switch st := s.(type) {
	case *ast.AssignStmt:
		n := *st
		u := f.pos(&n)
		n.Lhs, n.Rhs = f.exprs(st.Lhs), f.exprs(st.Rhs)
		f.done(&n, u)
		return &n
	case *ast.LocalAssignStmt:
		n := *st
		u := f.pos(&n)
		n.Exprs = f.exprs(st.Exprs)
		f.done(&n, u)
		return &n
	case *ast.FuncCallStmt:
		n := *st
		u := f.pos(&n)
		n.Expr = f.expr(st.Expr)
		f.done(&n, u)
		return &n
	case *ast.DoBlockStmt:
		n := *st
		u := f.pos(&n)
		n.Stmts = f.stmts(st.Stmts)
		f.done(&n, u)
		return &n
	case *ast.WhileStmt:
		n := *st
		u := f.pos(&n)
		n.Condition, n.Stmts = f.expr(st.Condition), f.stmts(st.Stmts)
		f.done(&n, u)
		return &n
	case *ast.RepeatStmt:
		n := *st
		u := f.pos(&n)
		n.Stmts = f.stmts(st.Stmts)
		n.Condition = f.expr(st.Condition)
		f.done(&n, u)
		return &n
	case *ast.IfStmt:
		n := *st
		u := f.pos(&n)
		n.Condition, n.Then = f.expr(st.Condition), f.stmts(st.Then)
		n.Else = f.stmts(st.Else)
		f.done(&n, u)
		return &n
	case *ast.NumberForStmt:
		n := *st
		u := f.pos(&n)
		n.Init, n.Limit = f.expr(st.Init), f.expr(st.Limit)
		if st.Step != nil {
			n.Step = f.expr(st.Step)
		}
		n.Stmts = f.stmts(st.Stmts)
		f.done(&n, u)
		return &n
	case *ast.GenericForStmt:
		n := *st
		u := f.pos(&n)
		n.Exprs, n.Stmts = f.exprs(st.Exprs), f.stmts(st.Stmts)
		f.done(&n, u)
		return &n
	case *ast.FuncDefStmt:
		n := *st
		u := f.pos(&n)
		name := *st.Name
		if name.Func != nil {
			name.Func = f.expr(name.Func)
		} else {
			name.Receiver = f.expr(name.Receiver)
		}
		n.Name = &name
		n.Func = f.expr(st.Func).(*ast.FunctionExpr)
		f.done(&n, u)
		return &n
	case *ast.ReturnStmt:
		n := *st
		u := f.pos(&n)
		n.Exprs = f.exprs(st.Exprs)
		f.done(&n, u)
		return &n
	case *ast.BreakStmt:
		n := *st
		f.pos(&n)
		return &n
	case *ast.LabelStmt:
		n := *st
		f.pos(&n)
		return &n
	case *ast.GotoStmt:
		n := *st
		f.pos(&n)
		return &n
	}
	panic(fmt.Sprintf("lineFiller: statement %T", s)) // checked by astCheck
}

func (f *lineFiller) expr(e ast.Expr) ast.Expr {
	switch ex := e.(type) {
	case *ast.TrueExpr:
		n := *ex
		f.pos(&n)
		return &n
	case *ast.FalseExpr:
		n := *ex
		f.pos(&n)
		return &n
	case *ast.NilExpr:
		n := *ex
		f.pos(&n)
		return &n
	case *ast.NumberExpr:
		n := *ex
		f.pos(&n)
		return &n
	case *ast.StringExpr:
		n := *ex
		f.pos(&n)
		return &n
	case *ast.CdataNumberExpr:
		n := *ex
		f.pos(&n)
		return &n
	case *ast.Comma3Expr:
		n := *ex
		f.pos(&n)
		return &n
	case *ast.IdentExpr:
		n := *ex
		f.pos(&n)
		return &n
	case *ast.AttrGetExpr:
		n := *ex
		u := f.pos(&n)
		n.Object, n.Key = f.expr(ex.Object), f.expr(ex.Key)
		f.done(&n, u)
		return &n
	case *ast.TableExpr:
		n := *ex
		u := f.pos(&n)
		n.Fields = make([]*ast.Field, len(ex.Fields))
		for i, fd := range ex.Fields {
			field := *fd // its span too
			if fd.Key != nil {
				field.Key = f.expr(fd.Key)
			}
			field.Value = f.expr(fd.Value)
			n.Fields[i] = &field
		}
		f.done(&n, u)
		return &n
	case *ast.FuncCallExpr:
		n := *ex
		u := f.pos(&n)
		if ex.Func != nil {
			n.Func = f.expr(ex.Func)
		} else {
			n.Receiver = f.expr(ex.Receiver)
		}
		n.Args = f.exprs(ex.Args)
		f.done(&n, u)
		return &n
	case *ast.LogicalOpExpr:
		n := *ex
		u := f.pos(&n)
		n.Lhs, n.Rhs = f.expr(ex.Lhs), f.expr(ex.Rhs)
		f.done(&n, u)
		return &n
	case *ast.RelationalOpExpr:
		n := *ex
		u := f.pos(&n)
		n.Lhs, n.Rhs = f.expr(ex.Lhs), f.expr(ex.Rhs)
		f.done(&n, u)
		return &n
	case *ast.StringConcatOpExpr:
		n := *ex
		u := f.pos(&n)
		n.Lhs, n.Rhs = f.expr(ex.Lhs), f.expr(ex.Rhs)
		f.done(&n, u)
		return &n
	case *ast.ArithmeticOpExpr:
		n := *ex
		u := f.pos(&n)
		n.Lhs, n.Rhs = f.expr(ex.Lhs), f.expr(ex.Rhs)
		f.done(&n, u)
		return &n
	case *ast.UnaryMinusOpExpr:
		n := *ex
		u := f.pos(&n)
		n.Expr = f.expr(ex.Expr)
		f.done(&n, u)
		return &n
	case *ast.UnaryNotOpExpr:
		n := *ex
		u := f.pos(&n)
		n.Expr = f.expr(ex.Expr)
		f.done(&n, u)
		return &n
	case *ast.UnaryLenOpExpr:
		n := *ex
		u := f.pos(&n)
		n.Expr = f.expr(ex.Expr)
		f.done(&n, u)
		return &n
	case *ast.FunctionExpr:
		n := *ex
		u := f.pos(&n)
		n.Stmts = f.stmts(ex.Stmts)
		f.done(&n, u)
		return &n
	}
	panic(fmt.Sprintf("lineFiller: expression %T", e)) // checked by astCheck
}

/* }}} */
