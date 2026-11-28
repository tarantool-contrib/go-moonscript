package ast

// go-moonscript patch: walking a syntax tree (see doc.go).

import "reflect"

// Visitor's Visit is called by Walk for each element of a tree. If it
// returns a visitor w, Walk visits the children of the element with w,
// then calls w.Visit(nil).
type Visitor interface {
	Visit(n Element) (w Visitor)
}

// Walk traverses a tree in source order, as go/ast.Walk does: it calls
// v.Visit(n); if that returns a visitor w other than nil, it walks each
// child of n with w, then calls w.Visit(nil). The children of an element
// are the elements it holds, in the order of the source:
//
//   - Chunk: Stmts;
//   - statements: AssignStmt: Lhs, Rhs; LocalAssignStmt: Exprs;
//     FuncCallStmt: Expr; DoBlockStmt: Stmts; WhileStmt: Condition, Stmts;
//     RepeatStmt: Stmts, Condition; IfStmt: Condition, Then, Else (an
//     "elseif" is the IfStmt in Else); NumberForStmt: Init, Limit, Step,
//     Stmts; GenericForStmt: Exprs, Stmts; FuncDefStmt: Name, Func;
//     ReturnStmt: Exprs; BreakStmt, LabelStmt, GotoStmt: none;
//   - expressions: AttrGetExpr: Object, Key; TableExpr: Fields;
//     FuncCallExpr: Func or Receiver, Args; the binary operators: Lhs,
//     Rhs; the unary ones: Expr; FunctionExpr: ParList, Stmts; the
//     constants, IdentExpr, Comma3Expr, CdataNumberExpr: none;
//   - Field: Key (nil for a positional item), Value; FuncName: Func or
//     Receiver; ParList: none.
//
// Names held as strings (local names, parameters, a method name, labels)
// are no elements: their spans are in the fields next to them. A block is
// no element either: its statements are children of the element holding
// it. Nil children, nil pointers among them, are skipped; an element of a
// type Walk doesn't know (an expression type of other code) has no
// children.
func Walk(v Visitor, n Element) {
	if isNilElement(n) {
		return
	}
	if v = v.Visit(n); v == nil {
		return
	}
	walkChildren(v, n)
	v.Visit(nil)
}

// Inspect traverses a tree in source order: it calls f(n), and if that
// returns true, inspects each child of n (see Walk). Like go-luascope's
// ast.Inspect, and unlike go/ast.Inspect, it does not call f(nil) after
// the children: a walk that needs to know where an element's children end
// uses Walk.
func Inspect(n Element, f func(Element) bool) {
	Walk(inspector(f), n)
}

type inspector func(Element) bool

func (f inspector) Visit(n Element) Visitor {
	if n != nil && f(n) {
		return f
	}
	return nil
}

// isNilElement reports whether n is nil or a nil pointer.
func isNilElement(n Element) bool {
	if n == nil {
		return true
	}
	v := reflect.ValueOf(n)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func walkStmts(v Visitor, stmts []Stmt) {
	for _, s := range stmts {
		Walk(v, s)
	}
}

func walkExprs(v Visitor, exprs []Expr) {
	for _, e := range exprs {
		Walk(v, e)
	}
}

// walkExpr walks e if it is not nil (an Expr interface holding nil is not
// an Element holding nil).
func walkExpr(v Visitor, e Expr) {
	if e != nil {
		Walk(v, e)
	}
}

func walkChildren(v Visitor, n Element) {
	switch n := n.(type) {
	case *Chunk:
		walkStmts(v, n.Stmts)

	case *AssignStmt:
		walkExprs(v, n.Lhs)
		walkExprs(v, n.Rhs)
	case *LocalAssignStmt:
		walkExprs(v, n.Exprs)
	case *FuncCallStmt:
		walkExpr(v, n.Expr)
	case *DoBlockStmt:
		walkStmts(v, n.Stmts)
	case *WhileStmt:
		walkExpr(v, n.Condition)
		walkStmts(v, n.Stmts)
	case *RepeatStmt:
		walkStmts(v, n.Stmts)
		walkExpr(v, n.Condition)
	case *IfStmt:
		walkExpr(v, n.Condition)
		walkStmts(v, n.Then)
		walkStmts(v, n.Else)
	case *NumberForStmt:
		walkExpr(v, n.Init)
		walkExpr(v, n.Limit)
		walkExpr(v, n.Step)
		walkStmts(v, n.Stmts)
	case *GenericForStmt:
		walkExprs(v, n.Exprs)
		walkStmts(v, n.Stmts)
	case *FuncDefStmt:
		if n.Name != nil {
			Walk(v, n.Name)
		}
		if n.Func != nil {
			Walk(v, n.Func)
		}
	case *ReturnStmt:
		walkExprs(v, n.Exprs)

	case *AttrGetExpr:
		walkExpr(v, n.Object)
		walkExpr(v, n.Key)
	case *TableExpr:
		for _, f := range n.Fields {
			if f != nil {
				Walk(v, f)
			}
		}
	case *Field:
		walkExpr(v, n.Key)
		walkExpr(v, n.Value)
	case *FuncCallExpr:
		walkExpr(v, n.Func)
		walkExpr(v, n.Receiver)
		walkExprs(v, n.Args)
	case *LogicalOpExpr:
		walkExpr(v, n.Lhs)
		walkExpr(v, n.Rhs)
	case *RelationalOpExpr:
		walkExpr(v, n.Lhs)
		walkExpr(v, n.Rhs)
	case *StringConcatOpExpr:
		walkExpr(v, n.Lhs)
		walkExpr(v, n.Rhs)
	case *ArithmeticOpExpr:
		walkExpr(v, n.Lhs)
		walkExpr(v, n.Rhs)
	case *UnaryMinusOpExpr:
		walkExpr(v, n.Expr)
	case *UnaryNotOpExpr:
		walkExpr(v, n.Expr)
	case *UnaryLenOpExpr:
		walkExpr(v, n.Expr)
	case *FunctionExpr:
		if n.ParList != nil {
			Walk(v, n.ParList)
		}
		walkStmts(v, n.Stmts)
	case *FuncName:
		walkExpr(v, n.Func)
		walkExpr(v, n.Receiver)
	}
}
