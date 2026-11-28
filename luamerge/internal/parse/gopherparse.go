// Package parse extracts require(...) call information from Lua source code
// using the gopher-lua parser. gopher-lua reads Lua 5.1; the source is read
// with a lexer that knows every dialect first, and gopher-lua gets a copy of
// it written in Lua 5.1 (see normalize).
package parse

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yuin/gopher-lua/ast"
	"github.com/yuin/gopher-lua/parse"

	"github.com/tarantool/go-luamerge/internal/lex"
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

// errBadMark is the cause of the error for a string of the marked copy whose
// mark names no string literal of the source (see markedCopy), which the copy
// never writes.
var errBadMark = errors.New("a string of the copy is marked with no literal of the source")

type gopherParser struct{}

// Parse reads source with the lexer, gives gopher-lua the copy of it written
// in Lua 5.1 (see normalize), each string literal marked with its token (see
// markedCopy), and walks the syntax tree for the uses of require. When
// gopher-lua cannot read the copy, the token scanner finds them instead (see
// scanRequires): a module must not fail the build because of its syntax. Only
// a lexical error does, as no dialect reads such a file.
func (p *gopherParser) Parse(source []byte, filename string) (Result, error) {
	tokens, err := significantTokens(source)
	if err != nil {
		return Result{}, fmt.Errorf("parsing lua source %q: %w", filename, err)
	}

	normalized := markedCopy(source, tokens)

	chunk, err := parse.Parse(bytes.NewReader(normalized), filename)
	if err != nil {
		return Result{Requires: scanRequires(source, tokens), Fallback: newFallback(err, normalized)}, nil
	}

	requires, err := findRequires(chunk, tokens)
	if err != nil {
		return Result{}, err
	}

	return Result{Requires: requires, Fallback: nil}, nil
}

// newFallback describes err, the error gopher-lua reports for normalized,
// the marked copy of a source, as the Fallback of the source. The copy has
// the lines of the source, but not its columns or its tokens, so only the
// line and the token gopher-lua read are kept, a string without its mark. At
// the end of the copy gopher-lua names no line: that is the line of the last
// token.
func newFallback(err error, normalized []byte) *Fallback {
	var syntaxErr *parse.Error
	if !errors.As(err, &syntaxErr) {
		// Not a syntax error: the parser failed some other way.
		return &Fallback{Line: 0, Reason: err.Error()}
	}

	message := strings.TrimSpace(syntaxErr.Message)
	if message != "" {
		message = strings.ToLower(message[:1]) + message[1:]
	}

	if syntaxErr.Pos.Line == parse.EOF {
		return &Fallback{Line: bytes.Count(normalized, []byte("\n")) + 1, Reason: message + " at the end of the file"}
	}

	token, _, _ := cutMark(syntaxErr.Token)

	return &Fallback{Line: syntaxErr.Pos.Line, Reason: fmt.Sprintf("%s near %q", message, token)}
}

// findRequires walks a parsed chunk and returns every use of require in it.
// It fails on a node type the walker does not know. When the chunk was read
// from the marked copy of a source (see markedCopy), tokens are the tokens of
// the source without its comments, and the literal of each static call has
// its position in the source; nil tokens leave every position unknown.
func findRequires(chunk []ast.Stmt, tokens []lex.Token) ([]RequireInfo, error) {
	walker := &requireWalker{
		requires: []RequireInfo{},
		err:      nil,
		scopes:   nil,
		tokens:   tokens,
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
	// tokens are the tokens of the source whose marked copy the chunk was
	// read from, or nil (see findRequires).
	tokens []lex.Token
}

// literal returns the value of the string str and where the string literal
// it was read from lies in the source; the zero Span when the chunk was not
// read from a marked copy, or when str was read from a name: gopher-lua reads
// `a.b` as `a["b"]`.
func (w *requireWalker) literal(str *ast.StringExpr) (string, Span) {
	if w.tokens == nil {
		return str.Value, Span{Start: 0, End: 0}
	}

	value, index, marked := cutMark(str.Value)
	if !marked {
		return value, Span{Start: 0, End: 0}
	}

	if index >= len(w.tokens) || w.tokens[index].Kind != lex.String && w.tokens[index].Kind != lex.LongString {
		if w.err == nil {
			w.err = fmt.Errorf("line %d: %w: %q", str.Line(), errBadMark, str.Value)
		}

		return value, Span{Start: 0, End: 0}
	}

	return value, Span{Start: w.tokens[index].Start, End: w.tokens[index].End}
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
	// gopher-lua gives `local function f` the line of its end as its last
	// line and `local f = function` no last line.
	if stmt.LastLine() != 0 && len(stmt.Names) == 1 {
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
	case *ast.IdentExpr, *ast.StringExpr, *ast.NumberExpr,
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
		Name: "", Line: call.Line(), Static: false, Literal: Span{Start: 0, End: 0}, Protected: protected,
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

// requireRef returns how expr names the global require function: "require",
// "_G.require" or "_ENV.require" (`_G["require"]` parses the same as
// `_G.require`), and "" when it names something else. The name require is
// the global require unless a local of that name hides it; _G and _ENV go by
// spelling.
func requireRef(w *requireWalker, expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.IdentExpr:
		if expr.Value == requireName && !w.hidden() {
			return expr.Value
		}
	case *ast.AttrGetExpr:
		table, isIdent := expr.Object.(*ast.IdentExpr)
		key, isString := expr.Key.(*ast.StringExpr)

		if !isIdent || !isString || table.Value != "_G" && table.Value != "_ENV" {
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
