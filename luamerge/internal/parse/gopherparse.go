// Package parse extracts require(...) call information from Lua source code
// using the parser of the gopher-lua fork of this repository (lua/parse): in
// its LuaJIT dialect, which reads what LuaJIT and Tarantool read, then in the
// syntax of LuaJIT's LJ_52 builds, then in its Lua 5.1 dialect.
package parse

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

// requireName is the name of Lua's global require function.
const requireName = "require"

// xpcallArgsStart is the index of the first argument that
// xpcall(f, handler, ...) passes on to f.
const xpcallArgsStart = 2

// errUnsupportedNode is the cause of the error for a syntax node of a type the
// require walker does not know: a require inside it would be missed, and a
// bundle missing a module must not be produced silently.
var errUnsupportedNode = errors.New("the require walker does not handle")

type gopherParser struct{}

// Parse reads source with the fork's parser and walks the syntax tree for the
// uses of require; the positions of the module names are the spans of their
// string literals. The parser reads the source in the dialects of the
// runtimes luamerge supports, the first that accepts it deciding:
//
//   - LuaJIT's, which Tarantool shares;
//   - LuaJIT's with the syntax of its LJ_52 builds (parse.Options.Lua52Compat):
//     empty statements, statements after break, calls across a line break and
//     goto as a keyword everywhere, where the first reads goto as a name too;
//   - Lua 5.1's, for what only it reads: an escape in a string that LuaJIT
//     does not know ("\."), and numerals that LuaJIT does not end where Lua
//     5.1 does ("1e5..x") or reads as out of range (1e1048576).
//
// When none accepts it, the token scanner finds the uses of require instead
// (see scanRequires): a module must not fail the build because of its syntax.
// Only a lexical error does, as no dialect reads such a file. The error in
// the warning is the one of LuaJIT's LJ_52 syntax, so that a file written for
// LuaJIT gets the message LuaJIT gives, but where its lexer refused a string
// or a numeral that Lua 5.1 reads: then it is the one of Lua 5.1's dialect,
// if that got further into the file.
func (p *gopherParser) Parse(source []byte, filename string) (Result, error) {
	text := readable(source)

	chunk, err := parse.ParseChunk(text, filename, luaJIT(false))
	if err == nil {
		return walk(chunk, nil)
	}

	chunk, err = parse.ParseChunk(text, filename, luaJIT(true))
	if err == nil {
		tokens, tokensErr := significantTokens(source)
		if tokensErr != nil {
			return Result{}, fmt.Errorf("parsing lua source %q: %w", filename, tokensErr)
		}

		return walk(chunk, &callSites{source: source, tokens: tokens})
	}

	chunk, err51 := parse.ParseChunk(source, filename, lua51())
	if err51 == nil {
		return walk(chunk, nil)
	}

	tokens, tokensErr := significantTokens(source)
	if tokensErr != nil {
		return Result{}, fmt.Errorf("parsing lua source %q: %w", filename, tokensErr)
	}

	return Result{Requires: scanRequires(source, tokens), Fallback: newFallback(reported(err, err51), tokens)}, nil
}

// reported returns the error that the warning for a file no parser reads
// names: luajit, the error of LuaJIT's parser, unless it is the refusal of a
// string with an escape or of a numeral that Lua 5.1 reads, and lua51, the
// error of Lua 5.1's parser, lies further into the file.
func reported(luajit, lua51 error) error {
	first, ok1 := errors.AsType[*parse.Error](luajit)
	second, ok2 := errors.AsType[*parse.Error](lua51)

	if !ok1 || !ok2 || first.Message != "invalid escape sequence" && first.Message != "malformed number" {
		return luajit
	}

	if second.Pos.Line > first.Pos.Line || second.Pos.Line == first.Pos.Line && second.Pos.Column > first.Pos.Column {
		return lua51
	}

	return luajit
}

// significantTokens returns the tokens of src without its comments, read the
// way the interpreter reads a file it loads (see lex.NewFile). The error is a
// lexical error, of source no dialect reads.
func significantTokens(src []byte) ([]lex.Token, error) {
	all, err := lex.NewFile(src).Tokens()
	if err != nil {
		return nil, err //nolint:wrapcheck // a *lex.Error, which says what went wrong and where.
	}

	tokens := make([]lex.Token, 0, len(all))

	for _, tok := range all {
		if tok.Kind != lex.Comment && tok.Kind != lex.LongComment {
			tokens = append(tokens, tok)
		}
	}

	return tokens, nil
}

// luaJIT returns the options of the fork's parser in its LuaJIT dialect,
// with the syntax of LuaJIT's LJ_52 builds when lua52 is set.
func luaJIT(lua52 bool) parse.Options {
	return parse.Options{Dialect: parse.LuaJIT, Comments: false, Shebang: false, Tokens: false, Lua52Compat: lua52}
}

// lua51 returns the options of the fork's parser in its Lua 5.1 dialect, as
// lua5.1 reads a file: it skips a first line that starts with "#".
func lua51() parse.Options {
	return parse.Options{Dialect: parse.Lua51, Comments: false, Shebang: true, Tokens: false, Lua52Compat: false}
}

// readable returns source as the fork's parser reads it the way the
// interpreter reads a file it loads (lex.Header): the fork's lexer skips a
// byte order mark and a first line that starts with "#" itself, but ends that
// line at a "\r" too, as LuaJIT does, where PUC Lua and luamerge end it at
// the first "\n" only. The header of such a line becomes spaces.
func readable(source []byte) []byte {
	header := lex.Header(source)
	if bytes.IndexByte(source[:header], '\r') < 0 {
		return source
	}

	readable := bytes.Clone(source)
	for i := range header {
		readable[i] = ' '
	}

	return readable
}

// walk returns the uses of require in chunk, read from a source. sites, when
// not nil, are the tokens of the source, which the syntax of LuaJIT's LJ_52
// builds needs to find the line of a call.
func walk(chunk *ast.Chunk, sites *callSites) (Result, error) {
	requires, err := findRequires(chunk.Stmts, chunk.Lines.Source(), sites)
	if err != nil {
		return Result{}, err
	}

	return Result{Requires: requires, Fallback: nil}, nil
}

// newFallback describes err, the error the fork's parser reports for a
// source whose tokens without comments are tokens, as the Fallback of the
// source: the line of the error, and at the end of the source, where the
// parser names the token "<eof>", the line of the last token; an error of a
// limit names no token.
func newFallback(err error, tokens []lex.Token) *Fallback {
	var syntaxErr *parse.Error
	if !errors.As(err, &syntaxErr) {
		// Not a syntax error: the parser failed some other way.
		return &Fallback{Line: 0, Reason: err.Error()}
	}

	message := strings.TrimSpace(syntaxErr.Message)
	if message != "" {
		message = strings.ToLower(message[:1]) + message[1:]
	}

	switch syntaxErr.Token {
	case "<eof>":
		line := syntaxErr.Pos.Line
		if len(tokens) > 0 {
			line = tokens[len(tokens)-1].Line
		}

		return &Fallback{Line: line, Reason: message + " at the end of the file"}
	case "":
		return &Fallback{Line: syntaxErr.Pos.Line, Reason: message}
	default:
		reason := fmt.Sprintf("%s near %q", message, errorToken(syntaxErr.Token))

		return &Fallback{Line: syntaxErr.Pos.Line, Reason: reason}
	}
}

// errorToken returns the token a syntax error of the parser names, a string
// as its value. The parser names a token as Lua prints it: a short string by
// its quotes around its value, a long one by its brackets around it, each up
// to the first NUL byte of the value.
func errorToken(token string) string {
	switch {
	case token == "":
		return token
	case token[0] == '"' || token[0] == '\'':
		return strings.TrimSuffix(token[1:], token[:1])
	case token[0] == '[':
		level := strings.IndexByte(token[1:], '[')
		if len(token) < len("[[") || level < 0 || strings.Trim(token[1:1+level], "=") != "" {
			return token
		}

		return strings.TrimSuffix(token[level+2:], "]"+strings.Repeat("=", level)+"]")
	default:
		return token
	}
}

// findRequires walks a parsed chunk, read from text, and returns every use of
// require in it. It fails on a node type the walker does not know. sites are
// those of the source the chunk was read from, or nil (see walk).
func findRequires(chunk []ast.Stmt, text []byte, sites *callSites) ([]RequireInfo, error) {
	walker := &requireWalker{
		requires: []RequireInfo{},
		err:      nil,
		scopes:   nil,
		text:     text,
		sites:    sites,
	}
	walkBlock(walker, chunk)

	if walker.err != nil {
		return nil, walker.err
	}

	return walker.requires, nil
}

type requireWalker struct {
	requires []RequireInfo
	// err reports the first node the walker could not look into.
	err error
	// scopes are the blocks the walk is in, the innermost last.
	scopes []scope
	// text is the text the chunk was read from.
	text []byte
	// sites are the tokens of the source, or nil when a call is on the line
	// the parser numbers it by.
	sites *callSites
}

// callSites are the tokens of a source that LuaJIT's LJ_52 builds read.
type callSites struct {
	source []byte
	// tokens are the tokens of source without its comments.
	tokens []lex.Token
}

// isOpenParen reports whether tok, a token of the source, is "(".
func (c *callSites) isOpenParen(tok lex.Token) bool {
	return tok.Kind == lex.Symbol && string(c.source[tok.Start:tok.End]) == "("
}

// literal returns the value of the string str and where the string literal
// it was read from lies in the source; the zero Span when str was read from
// a name: the parser reads `a.b` as `a["b"]`, and a string literal starts
// with its quote or its long bracket.
func (w *requireWalker) literal(str *ast.StringExpr) (string, Span) {
	span := str.Span()
	if span.Start >= span.End || !strings.ContainsRune(`"'[`, rune(w.text[span.Start])) {
		return str.Value, Span{Start: 0, End: 0}
	}

	return str.Value, Span{Start: span.Start, End: span.End}
}

// callLine returns the line of call that a warning about it names: the line
// of its arguments, as the parser numbers it. In the syntax of LuaJIT's LJ_52
// builds, a "(" of the arguments that starts a line is a call across a line
// break, which only those builds and Lua 5.2 and later read, and the call is
// on the line of the token before it, the end of the called expression.
func (w *requireWalker) callLine(call *ast.FuncCallExpr) int {
	line := call.Line()
	if w.sites == nil || call.Func == nil {
		return line
	}

	tokens := w.sites.tokens
	after := call.Func.OuterSpan().End
	i := sort.Search(len(tokens), func(i int) bool { return tokens[i].Start >= after })

	if i > 0 && i < len(tokens) && w.sites.isOpenParen(tokens[i]) && tokens[i].Line > tokens[i-1].Line {
		return tokens[i-1].Line
	}

	return line
}

// scope is what a block declares about the name require. A local named
// require that is a function of its own, a local function, a local whose
// value is a function, a parameter or a loop variable, hides the global
// require from its declaration to the end of its block. A local of any other
// value is the global require, `local require = require`, or a wrapper of
// it, such as a lazy require (`local require = require("lazy")`), which loads
// the module of the name it gets: the calls through it are found as before.
type scope struct {
	// declared is set when the block declares a local named require.
	declared bool
	// global is set when that local is the global require or a wrapper of it.
	global bool
}

// enter starts a block.
func (w *requireWalker) enter() {
	w.scopes = append(w.scopes, scope{declared: false, global: false})
}

// leave ends the block entered last.
func (w *requireWalker) leave() {
	w.scopes = w.scopes[:len(w.scopes)-1]
}

// declare declares the local name in the current block; global says whether
// it is the global require or a wrapper of it.
func (w *requireWalker) declare(name string, global bool) {
	if name == requireName {
		w.scopes[len(w.scopes)-1] = scope{declared: true, global: global}
	}
}

// hidden reports whether a local named require that does not hold the global
// require is in scope, so that the name require is not the global.
func (w *requireWalker) hidden() bool {
	for _, block := range slices.Backward(w.scopes) {
		if block.declared {
			return !block.global
		}
	}

	return false
}

func walkStmts(w *requireWalker, stmts []ast.Stmt) {
	for _, stmt := range stmts {
		walkStmt(w, stmt)
	}
}

// walkBlock walks stmts as a block of their own.
func walkBlock(w *requireWalker, stmts []ast.Stmt) {
	w.enter()
	walkStmts(w, stmts)
	w.leave()
}

// walkFunction walks the body of a function, where its parameters are
// locals.
func walkFunction(w *requireWalker, function *ast.FunctionExpr) {
	w.enter()

	for _, name := range function.ParList.Names {
		w.declare(name, false)
	}

	walkStmts(w, function.Stmts)
	w.leave()
}

func walkStmt(w *requireWalker, stmt ast.Stmt) {
	switch stmt := stmt.(type) {
	case *ast.AssignStmt:
		walkAssign(w, stmt)
	case *ast.LocalAssignStmt:
		walkLocalAssign(w, stmt)
	case *ast.FuncCallStmt:
		walkExpr(w, stmt.Expr)
	case *ast.DoBlockStmt:
		walkBlock(w, stmt.Stmts)
	case *ast.WhileStmt:
		walkExpr(w, stmt.Condition)
		walkBlock(w, stmt.Stmts)
	case *ast.RepeatStmt:
		// The condition is in the scope of the body's locals.
		w.enter()
		walkStmts(w, stmt.Stmts)
		walkExpr(w, stmt.Condition)
		w.leave()
	case *ast.IfStmt:
		// An elseif is an IfStmt nested in Else.
		walkExpr(w, stmt.Condition)
		walkBlock(w, stmt.Then)
		walkBlock(w, stmt.Else)
	case *ast.NumberForStmt:
		walkExpr(w, stmt.Init)
		walkExpr(w, stmt.Limit)
		walkExpr(w, stmt.Step)
		w.enter()
		w.declare(stmt.Name, false)
		walkStmts(w, stmt.Stmts)
		w.leave()
	case *ast.GenericForStmt:
		walkExprs(w, stmt.Exprs)
		w.enter()

		for _, name := range stmt.Names {
			w.declare(name, false)
		}

		walkStmts(w, stmt.Stmts)
		w.leave()
	case *ast.FuncDefStmt:
		// The name, `a.b:c`, is made of identifiers only.
		walkFunction(w, stmt.Func)
	case *ast.ReturnStmt:
		walkExprs(w, stmt.Exprs)
	case *ast.BreakStmt, *ast.LabelStmt, *ast.GotoStmt:
		// No children.
	default:
		unsupported(w, stmt)
	}
}

// walkAssign walks an assignment. Storing require where the walker finds its
// calls by name (`require = require`, `_G.require = require`) is not a use of
// it; storing it anywhere else is, a local named require included.
func walkAssign(w *requireWalker, stmt *ast.AssignStmt) {
	for _, target := range stmt.Lhs {
		walkTarget(w, target)
	}

	for i, value := range stmt.Rhs {
		if i < len(stmt.Lhs) && isRequire(w, value) {
			if !isRequire(w, stmt.Lhs[i]) {
				recordIndirect(w, value, "assigned to "+describeTarget(stmt.Lhs[i]))
			}

			continue
		}

		walkExpr(w, value)
	}
}

// walkLocalAssign walks a local declaration. `local require = require` caches
// the global, and calls through the local are still found by name, so only
// binding require to another name is a use of it. The names come into scope
// after the declaration, but the name of a local function is in scope in its
// own body.
func walkLocalAssign(w *requireWalker, stmt *ast.LocalAssignStmt) {
	if stmt.IsFunction {
		w.declare(stmt.Names[0], false)
		walkExprs(w, stmt.Exprs)

		return
	}

	global := make([]bool, len(stmt.Names))
	for i := range stmt.Names {
		global[i] = i >= len(stmt.Exprs) || !hidesRequire(w, stmt.Exprs[i])
	}

	for i, value := range stmt.Exprs {
		if i < len(stmt.Names) && isRequire(w, value) {
			if stmt.Names[i] != requireName {
				recordIndirect(w, value, "aliased as local "+strconv.Quote(stmt.Names[i]))
			}

			continue
		}

		walkExpr(w, value)
	}

	for i, name := range stmt.Names {
		w.declare(name, global[i])
	}
}

// hidesRequire reports whether a local named require whose value is value
// hides the global require: when value is a function of its own, or the name
// require where a local that hides the global is in scope. Any other value is
// the global require or a wrapper of it (see scope).
func hidesRequire(w *requireWalker, value ast.Expr) bool {
	switch value := value.(type) {
	case *ast.FunctionExpr:
		return true
	case *ast.IdentExpr:
		return value.Value == requireName && w.hidden()
	default:
		return false
	}
}

// walkTarget walks the target of an assignment. A name assigned to is not a
// use of its value, but a table field's object and key are evaluated:
// `t[require("x")] = v`.
func walkTarget(w *requireWalker, target ast.Expr) {
	field, ok := target.(*ast.AttrGetExpr)
	if ok {
		walkExpr(w, field.Object)
		walkExpr(w, field.Key)
	}
}

func walkExprs(w *requireWalker, exprs []ast.Expr) {
	for _, expr := range exprs {
		walkExpr(w, expr)
	}
}

func walkExpr(w *requireWalker, expr ast.Expr) {
	// A require reached here is neither called by the expression around it
	// nor bound to a name: it is passed on, stored or returned. Nothing inside
	// `require` or `_G.require` can be a use of require, so the walk goes on.
	if isRequire(w, expr) {
		recordIndirect(w, expr, "used as a value")
	}

	switch expr := expr.(type) {
	case nil:
		// An absent optional child: a plain call's Receiver, a method call's
		// Func, a numeric for's step, an array item's key.
	case *ast.FuncCallExpr:
		walkCall(w, expr)
	case *ast.FunctionExpr:
		walkFunction(w, expr)
	case *ast.AttrGetExpr:
		walkExpr(w, expr.Object)
		walkExpr(w, expr.Key)
	case *ast.TableExpr:
		for _, field := range expr.Fields {
			walkExpr(w, field.Key)
			walkExpr(w, field.Value)
		}
	case *ast.ArithmeticOpExpr:
		walkExpr(w, expr.Lhs)
		walkExpr(w, expr.Rhs)
	case *ast.RelationalOpExpr:
		walkExpr(w, expr.Lhs)
		walkExpr(w, expr.Rhs)
	case *ast.LogicalOpExpr:
		walkExpr(w, expr.Lhs)
		walkExpr(w, expr.Rhs)
	case *ast.StringConcatOpExpr:
		walkExpr(w, expr.Lhs)
		walkExpr(w, expr.Rhs)
	case *ast.UnaryMinusOpExpr:
		walkExpr(w, expr.Expr)
	case *ast.UnaryNotOpExpr:
		walkExpr(w, expr.Expr)
	case *ast.UnaryLenOpExpr:
		walkExpr(w, expr.Expr)
	case *ast.IdentExpr, *ast.StringExpr, *ast.NumberExpr, *ast.CdataNumberExpr,
		*ast.TrueExpr, *ast.FalseExpr, *ast.NilExpr, *ast.Comma3Expr:
		// Leaf expressions, no children.
	default:
		unsupported(w, expr)
	}
}

// unsupported records node, of a type the walker does not know, as the
// walker's error, unless an earlier node already is one.
func unsupported(w *requireWalker, node ast.PositionHolder) {
	if w.err == nil {
		w.err = fmt.Errorf("line %d: %w %T", node.Line(), errUnsupportedNode, node)
	}
}

// walkCall walks a function call. The expression that stands for require in a
// call of require is not walked, so it is not taken for a use of require as a
// value.
func walkCall(w *requireWalker, call *ast.FuncCallExpr) {
	requireExpr := checkRequireCall(w, call)
	// A method call `obj:m(...)` has a Receiver instead of a Func, and the
	// receiver can hold a require: `require("x"):new()`.
	walkExpr(w, call.Receiver)

	if call.Func != requireExpr {
		walkExpr(w, call.Func)
	}

	for _, arg := range call.Args {
		if arg != requireExpr {
			walkExpr(w, arg)
		}
	}
}

// checkRequireCall records call if it calls require: `require(...)`,
// `_G.require(...)`, `pcall(require, ...)` or `xpcall(require, handler, ...)`.
// The call is static when the first argument require gets is a string literal
// (require ignores the others) and dynamic otherwise, including when it gets
// none; it is protected when pcall or xpcall calls require. It returns the
// expression that stands for require in the call, or nil when call does not
// call require.
func checkRequireCall(w *requireWalker, call *ast.FuncCallExpr) ast.Expr {
	requireExpr, args, protected := requireCallArgs(w, call)
	if requireExpr == nil {
		return nil
	}

	info := RequireInfo{
		Name: "", Line: w.callLine(call), Static: false, Literal: Span{Start: 0, End: 0}, Protected: protected,
		Indirect: "",
	}

	if len(args) > 0 {
		if str, ok := args[0].(*ast.StringExpr); ok {
			info.Name, info.Literal = w.literal(str)
			info.Static = true
		}
	}

	w.requires = append(w.requires, info)

	return requireExpr
}

// requireCallArgs returns the expression that stands for require in call, the
// arguments require is called with and whether pcall or xpcall calls it in
// protected mode; or nil, nil and false when call does not call require.
func requireCallArgs(w *requireWalker, call *ast.FuncCallExpr) (ast.Expr, []ast.Expr, bool) {
	switch {
	case isRequire(w, call.Func):
		return call.Func, call.Args, false
	case callsGlobal(call, "pcall") && len(call.Args) > 0 && isRequire(w, call.Args[0]):
		return call.Args[0], call.Args[1:], true
	case callsGlobal(call, "xpcall") && len(call.Args) > 0 && isRequire(w, call.Args[0]):
		// The call xpcall(f, handler, ...) calls f(...). PUC Lua 5.1's xpcall
		// passes f no arguments, so there the call fails natively; embedding
		// the module changes nothing about that.
		if len(call.Args) <= xpcallArgsStart {
			return call.Args[0], nil, true
		}

		return call.Args[0], call.Args[xpcallArgsStart:], true
	default:
		return nil, nil, false
	}
}

// requireRef returns how expr names the global require function: "require" or
// "_G.require" (`_G["require"]` parses the same as `_G.require`), and ""
// when it names something else. The name require is the global require
// unless a local of that name hides it; _G goes by spelling.
func requireRef(w *requireWalker, expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.IdentExpr:
		if expr.Value == requireName && !w.hidden() {
			return expr.Value
		}
	case *ast.AttrGetExpr:
		table, isIdent := expr.Object.(*ast.IdentExpr)
		key, isString := expr.Key.(*ast.StringExpr)

		if !isIdent || !isString || table.Value != "_G" {
			return ""
		}

		if name, _ := w.literal(key); name == requireName {
			return table.Value + "." + name
		}
	}

	return ""
}

// isRequire reports whether expr names the global require function; see
// requireRef.
func isRequire(w *requireWalker, expr ast.Expr) bool {
	return requireRef(w, expr) != ""
}

// callsGlobal reports whether call calls the global function name directly.
func callsGlobal(call *ast.FuncCallExpr, name string) bool {
	fn, ok := call.Func.(*ast.IdentExpr)

	return ok && fn.Value == name
}

// recordIndirect records ref, which names require, as a use of require other
// than a call; how says what is done with it.
func recordIndirect(w *requireWalker, ref ast.Expr, how string) {
	w.requires = append(w.requires, RequireInfo{
		Name:      "",
		Line:      ref.Line(),
		Static:    false,
		Literal:   Span{Start: 0, End: 0},
		Protected: false,
		Indirect:  requireRef(w, ref) + " " + how,
	})
}

// describeTarget names an assignment target for a warning.
func describeTarget(target ast.Expr) string {
	name, ok := target.(*ast.IdentExpr)
	if ok {
		return strconv.Quote(name.Value)
	}

	return "a table field"
}
