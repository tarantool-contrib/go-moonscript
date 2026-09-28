// Package parse extracts require(...) call information from Lua source code
// using the gopher-lua parser.
package parse

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"github.com/yuin/gopher-lua/ast"
	"github.com/yuin/gopher-lua/parse"
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

func (p *gopherParser) Parse(source []byte, filename string) ([]RequireInfo, error) {
	reader := bytes.NewReader(source)

	chunk, err := parse.Parse(reader, filename)
	if err != nil {
		return nil, fmt.Errorf("parsing lua source %q: %w", filename, err)
	}

	return findRequires(chunk)
}

// findRequires walks a parsed chunk and returns every use of require in it.
// It fails on a node type the walker does not know.
func findRequires(chunk []ast.Stmt) ([]RequireInfo, error) {
	walker := &requireWalker{
		requires: []RequireInfo{},
		err:      nil,
	}
	walkStmts(walker, chunk)

	if walker.err != nil {
		return nil, walker.err
	}

	return walker.requires, nil
}

type requireWalker struct {
	requires []RequireInfo
	// err reports the first node the walker could not look into.
	err error
}

func walkStmts(w *requireWalker, stmts []ast.Stmt) {
	for _, stmt := range stmts {
		walkStmt(w, stmt)
	}
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
		walkStmts(w, stmt.Stmts)
	case *ast.WhileStmt:
		walkExpr(w, stmt.Condition)
		walkStmts(w, stmt.Stmts)
	case *ast.RepeatStmt:
		walkStmts(w, stmt.Stmts)
		walkExpr(w, stmt.Condition)
	case *ast.IfStmt:
		// An elseif is an IfStmt nested in Else.
		walkExpr(w, stmt.Condition)
		walkStmts(w, stmt.Then)
		walkStmts(w, stmt.Else)
	case *ast.NumberForStmt:
		walkExpr(w, stmt.Init)
		walkExpr(w, stmt.Limit)
		walkExpr(w, stmt.Step)
		walkStmts(w, stmt.Stmts)
	case *ast.GenericForStmt:
		walkExprs(w, stmt.Exprs)
		walkStmts(w, stmt.Stmts)
	case *ast.FuncDefStmt:
		// The name, `a.b:c`, is made of identifiers only.
		walkStmts(w, stmt.Func.Stmts)
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
// it; storing it anywhere else is.
func walkAssign(w *requireWalker, stmt *ast.AssignStmt) {
	for _, target := range stmt.Lhs {
		walkTarget(w, target)
	}

	for i, value := range stmt.Rhs {
		if i < len(stmt.Lhs) && isRequire(value) {
			if !isRequire(stmt.Lhs[i]) {
				recordIndirect(w, value, "assigned to "+describeTarget(stmt.Lhs[i]))
			}

			continue
		}

		walkExpr(w, value)
	}
}

// walkLocalAssign walks a local declaration. `local require = require` caches
// the global, and calls through the local are still found by name, so only
// binding require to another name is a use of it.
func walkLocalAssign(w *requireWalker, stmt *ast.LocalAssignStmt) {
	for i, value := range stmt.Exprs {
		if i < len(stmt.Names) && isRequire(value) {
			if stmt.Names[i] != requireName {
				recordIndirect(w, value, "aliased as local "+strconv.Quote(stmt.Names[i]))
			}

			continue
		}

		walkExpr(w, value)
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
	if isRequire(expr) {
		recordIndirect(w, expr, "used as a value")
	}

	switch expr := expr.(type) {
	case nil:
		// An absent optional child: a plain call's Receiver, a method call's
		// Func, a numeric for's step, an array item's key.
	case *ast.FuncCallExpr:
		walkCall(w, expr)
	case *ast.FunctionExpr:
		walkStmts(w, expr.Stmts)
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
	requireExpr, args, protected := requireCallArgs(call)
	if requireExpr == nil {
		return nil
	}

	info := RequireInfo{Name: "", Line: call.Line(), Static: false, Protected: protected, Indirect: ""}

	if len(args) > 0 {
		if str, ok := args[0].(*ast.StringExpr); ok {
			info.Name, info.Static = str.Value, true
		}
	}

	w.requires = append(w.requires, info)

	return requireExpr
}

// requireCallArgs returns the expression that stands for require in call, the
// arguments require is called with and whether pcall or xpcall calls it in
// protected mode; or nil, nil and false when call does not call require.
func requireCallArgs(call *ast.FuncCallExpr) (ast.Expr, []ast.Expr, bool) {
	switch {
	case isRequire(call.Func):
		return call.Func, call.Args, false
	case callsGlobal(call, "pcall") && len(call.Args) > 0 && isRequire(call.Args[0]):
		return call.Args[0], call.Args[1:], true
	case callsGlobal(call, "xpcall") && len(call.Args) > 0 && isRequire(call.Args[0]):
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
// `_G.require`), and "" when it names something else. Like the rest of the
// walker it goes by spelling: a local named require counts as the global.
func requireRef(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.IdentExpr:
		if expr.Value == requireName {
			return expr.Value
		}
	case *ast.AttrGetExpr:
		table, isIdent := expr.Object.(*ast.IdentExpr)
		key, isString := expr.Key.(*ast.StringExpr)

		if isIdent && isString && key.Value == requireName &&
			(table.Value == "_G" || table.Value == "_ENV") {
			return table.Value + "." + key.Value
		}
	}

	return ""
}

// isRequire reports whether expr names the global require function; see
// requireRef.
func isRequire(expr ast.Expr) bool {
	return requireRef(expr) != ""
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
		Protected: false,
		Indirect:  requireRef(ref) + " " + how,
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
