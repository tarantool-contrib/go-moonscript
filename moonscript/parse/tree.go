package parse

// Port of moonscript/parse/tree.moon: helpers used by the grammar callbacks.

import (
	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
)

// AssignError is raised (as a panic) by formatAssign for invalid assignment
// targets; Parse turns it into a formatted error. Mirrors error({node, msg}).
type AssignError struct {
	Node any
	Msg  string
}

var chainAssignable = map[string]bool{"index": true, "dot": true, "slice": true}

func isAssignable(node any) bool {
	if s, ok := node.(string); ok && s == "..." {
		return false
	}
	switch ast.NType(node) {
	case "ref", "self", "value", "self_class", "table":
		return true
	case "chain":
		return chainAssignable[ast.NType(node.(*ast.Node).Last())]
	}
	return false
}

func flattenExplist(tbl *ast.Node) any {
	if tbl.Len() == 1 {
		return tbl.At(1)
	}
	tbl.Insert(1, "explist")
	return tbl
}

func formatAssign(lhs *ast.Node, assign any) any {
	if assign == nil {
		return flattenExplist(lhs)
	}
	for _, e := range lhs.Items {
		if !isAssignable(e) {
			panic(AssignError{e, "left hand expression is not assignable"})
		}
	}
	a := assign.(*ast.Node)
	switch a.Kind() {
	case "assign":
		return ast.New(append([]any{"assign", lhs}, a.Rest(2)...)...)
	case "update":
		return ast.New(append([]any{"update", lhs.At(1)}, a.Rest(2)...)...)
	}
	panic("unknown assign expression: " + a.Kind())
}

func formatSingleAssign(lhs, assign any) any {
	if assign != nil {
		return formatAssign(ast.New(lhs), assign)
	}
	return lhs
}

func joinChain(callee any, args *ast.Node) any {
	if args.Len() == 0 {
		return callee
	}
	call := ast.New("call", args)
	if ast.NType(callee) == "chain" {
		c := callee.(*ast.Node)
		c.Append(call)
		return c
	}
	return ast.New("chain", callee, call)
}
