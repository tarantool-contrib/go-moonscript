package ast

import "math"

type Expr interface {
	PositionHolder
	Element
	// Parens are the parentheses around the expression (go-moonscript
	// patch).
	Parens() Parens
	SetParens(Parens)
	// OuterSpan is the span of the expression with its parentheses: that
	// of the outermost pair if it has any, else its Span (go-moonscript
	// patch).
	OuterSpan() Span
	exprMarker()
}

// ExprBase is what every expression embeds: the node, and the parentheses
// around it (go-moonscript patch), kept out of line as few expressions
// have any.
type ExprBase struct {
	Node
	parens *Parens
}

func (expr *ExprBase) exprMarker() {}

// Parens returns the parentheses around the expression, the zero Parens if
// it has none. go-moonscript patch.
func (expr *ExprBase) Parens() Parens {
	if expr.parens == nil {
		return Parens{}
	}
	return *expr.parens
}

// SetParens sets the parentheses around the expression; a Count of 0 or
// less removes them. go-moonscript patch.
func (expr *ExprBase) SetParens(p Parens) {
	if p.Count <= 0 {
		expr.parens = nil
		return
	}
	expr.parens = &p
}

// OuterSpan returns the span of the expression with its parentheses.
// go-moonscript patch.
func (expr *ExprBase) OuterSpan() Span {
	if expr.parens != nil {
		return expr.parens.Span
	}
	return expr.Span()
}

// Parens are the parentheses around an expression: Count pairs, the
// outermost from the "(" at Span.Start to the ")" ending at Span.End.
// They truncate a call or "..." to one value, which the compiler reads
// from FuncCallExpr.AdjustRet and Comma3Expr.AdjustRet (it ignores
// Parens), and make an expression no assignment target. go-moonscript
// patch.
type Parens struct {
	Count int
	Span  Span
}

/* ConstExprs {{{ */

type ConstExpr interface {
	Expr
	constExprMarker()
}

type ConstExprBase struct {
	ExprBase
}

func (expr *ConstExprBase) constExprMarker() {}

type TrueExpr struct {
	ConstExprBase
}

type FalseExpr struct {
	ConstExprBase
}

type NilExpr struct {
	ConstExprBase
}

type NumberExpr struct {
	ConstExprBase

	Value string
}

type StringExpr struct {
	ConstExprBase

	Value string
}

/* ConstExprs }}} */

// CdataNumberExpr is a numeral LuaJIT reads as cdata, accepted in the
// LuaJIT dialect: a 64-bit integer (1LL, 1ULL, 1LLU) or an imaginary number
// (1i). Value is its text. The VM has no FFI: evaluating one is an error.
// go-moonscript patch.
type CdataNumberExpr struct {
	ExprBase

	Value string
}

type Comma3Expr struct {
	ExprBase
	AdjustRet bool
}

type IdentExpr struct {
	ExprBase

	Value string
}

type AttrGetExpr struct {
	ExprBase

	Object Expr
	Key    Expr
}

type TableExpr struct {
	ExprBase

	Fields []*Field
	// go-moonscript patch: the line of the token before the "{" (luac
	// creates the table before reading the brace)
	PrevLine int
}

type FuncCallExpr struct {
	ExprBase

	Func      Expr
	Receiver  Expr
	Method    string
	Args      []Expr
	AdjustRet bool
	// go-moonscript patch: the start of the name Method, 0 if unknown (a
	// method name never starts a source), in the padding after AdjustRet
	methodStart uint32
	ArgsLine    int // go-moonscript patch: the line where the arguments start (0: unknown)
	// go-moonscript patch: the line where the arguments end (0: unknown)
	ArgsEndLine int
}

// MethodSpan returns the span of the name Method, the zero Span if
// unknown. go-moonscript patch.
func (expr *FuncCallExpr) MethodSpan() Span {
	if expr.methodStart == 0 {
		return Span{}
	}
	return Span{Start: int(expr.methodStart), End: int(expr.methodStart) + len(expr.Method)}
}

// SetMethodSpan sets the span of the name Method: its start, the end being
// that of Method. go-moonscript patch.
func (expr *FuncCallExpr) SetMethodSpan(s Span) {
	expr.methodStart = 0
	if s.Start > 0 && uint64(s.Start) <= math.MaxUint32 {
		expr.methodStart = uint32(s.Start)
	}
}

type LogicalOpExpr struct {
	ExprBase

	Operator string
	Lhs      Expr
	Rhs      Expr
}

type RelationalOpExpr struct {
	ExprBase

	Operator string
	Lhs      Expr
	Rhs      Expr
}

type StringConcatOpExpr struct {
	ExprBase

	Lhs Expr
	Rhs Expr
}

type ArithmeticOpExpr struct {
	ExprBase

	Operator string
	Lhs      Expr
	Rhs      Expr
}

type UnaryMinusOpExpr struct {
	ExprBase
	Expr Expr
}

type UnaryNotOpExpr struct {
	ExprBase
	Expr Expr
}

type UnaryLenOpExpr struct {
	ExprBase
	Expr Expr
}

// FunctionExpr is a function: "function (...) ... end" spans from the
// "function", the body of a function statement (FuncDefStmt, "local
// function") from the "(" of its parameters, to the "end"
// (go-moonscript patch: the spans).
type FunctionExpr struct {
	ExprBase

	ParList *ParList
	Stmts   []Stmt
	// StmtsSpan is the span of the body, Stmts (go-moonscript patch).
	StmtsSpan Span
}
