// Package parse extracts require(...) call information from Lua source code
// using the gopher-lua parser.
package parse

import (
	"bytes"
	"fmt"

	"github.com/yuin/gopher-lua/ast"
	"github.com/yuin/gopher-lua/parse"
)

type gopherParser struct{}

func (p *gopherParser) Parse(source []byte, filename string) ([]RequireInfo, error) {
	reader := bytes.NewReader(source)

	chunk, err := parse.Parse(reader, filename)
	if err != nil {
		return nil, fmt.Errorf("parsing lua source %q: %w", filename, err)
	}

	walker := &requireWalker{
		requires: []RequireInfo{},
	}
	walkStmts(walker, chunk)

	return walker.requires, nil
}

type requireWalker struct {
	requires []RequireInfo
}

func walkStmts(w *requireWalker, stmts []ast.Stmt) {
	for _, stmt := range stmts {
		walkStmt(w, stmt)
	}
}

func walkStmt(w *requireWalker, stmt ast.Stmt) {
	switch stmt := stmt.(type) {
	case *ast.AssignStmt:
		walkExprs(w, stmt.Lhs)
		walkExprs(w, stmt.Rhs)
	case *ast.LocalAssignStmt:
		walkExprs(w, stmt.Exprs)
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
		walkExpr(w, stmt.Condition)
		walkStmts(w, stmt.Then)

		if stmt.Else != nil {
			walkStmts(w, stmt.Else)
		}
	case *ast.NumberForStmt:
		walkExpr(w, stmt.Init)
		walkExpr(w, stmt.Limit)

		if stmt.Step != nil {
			walkExpr(w, stmt.Step)
		}

		walkStmts(w, stmt.Stmts)
	case *ast.GenericForStmt:
		walkExprs(w, stmt.Exprs)
		walkStmts(w, stmt.Stmts)
	case *ast.FuncDefStmt:
		walkStmts(w, stmt.Func.Stmts)
	case *ast.ReturnStmt:
		walkExprs(w, stmt.Exprs)
	case *ast.BreakStmt:
		// No children.
	}
}

func walkExprs(w *requireWalker, exprs []ast.Expr) {
	for _, expr := range exprs {
		walkExpr(w, expr)
	}
}

func walkExpr(w *requireWalker, expr ast.Expr) {
	switch expr := expr.(type) {
	case *ast.FuncCallExpr:
		checkRequireCall(w, expr)
		walkExpr(w, expr.Func)
		walkExprs(w, expr.Args)
	case *ast.FunctionExpr:
		walkStmts(w, expr.Stmts)
	case *ast.AttrGetExpr:
		walkExpr(w, expr.Object)
		walkExpr(w, expr.Key)
	case *ast.TableExpr:
		for _, field := range expr.Fields {
			if field.Key != nil {
				walkExpr(w, field.Key)
			}

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
		// Unknown expression type, ignore.
	}
}

// minPcallRequireArgs is the minimum argument count for pcall(require, "name", ...)
// to be recognized as a require call.
const minPcallRequireArgs = 2

func checkRequireCall(w *requireWalker, call *ast.FuncCallExpr) {
	ident, ok := call.Func.(*ast.IdentExpr)
	if !ok {
		return
	}

	switch ident.Value {
	case "require":
		checkDirectRequireCall(w, call)
	case "pcall":
		checkPcallRequireCall(w, call)
	}
}

// checkDirectRequireCall handles the require("...") pattern.
func checkDirectRequireCall(w *requireWalker, call *ast.FuncCallExpr) {
	if len(call.Args) == 1 {
		if str, ok := call.Args[0].(*ast.StringExpr); ok {
			w.requires = append(w.requires, RequireInfo{
				Name:   str.Value,
				Line:   call.Line(),
				Static: true,
			})

			return
		}
	}

	// Dynamic require: argument is not a single string literal.
	w.requires = append(w.requires, RequireInfo{
		Name:   "",
		Line:   call.Line(),
		Static: false,
	})
}

// checkPcallRequireCall handles the pcall(require, "...") pattern.
func checkPcallRequireCall(w *requireWalker, call *ast.FuncCallExpr) {
	if len(call.Args) < minPcallRequireArgs {
		return
	}

	reqIdent, ok := call.Args[0].(*ast.IdentExpr)
	if !ok || reqIdent.Value != "require" {
		return
	}

	str, ok := call.Args[1].(*ast.StringExpr)
	if !ok {
		return
	}

	w.requires = append(w.requires, RequireInfo{
		Name:   str.Value,
		Line:   call.Line(),
		Static: true,
	})
}
