package ast

type Expr interface {
	PositionHolder
	exprMarker()
}

type ExprBase struct {
	Node
}

func (expr *ExprBase) exprMarker() {}

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
	ArgsLine  int // go-moonscript patch: the line where the arguments start (0: unknown)
	// go-moonscript patch: the line where the arguments end (0: unknown)
	ArgsEndLine int
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

type FunctionExpr struct {
	ExprBase

	ParList *ParList
	Stmts   []Stmt
}
