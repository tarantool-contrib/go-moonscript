package ast

type Stmt interface {
	PositionHolder
	Element
	stmtMarker()
}

type StmtBase struct {
	Node
}

func (stmt *StmtBase) stmtMarker() {}

type AssignStmt struct {
	StmtBase

	Lhs []Expr
	Rhs []Expr
}

type LocalAssignStmt struct {
	StmtBase

	Names []string
	Exprs []Expr
	// NameSpans are the spans of Names, if known (go-moonscript patch).
	NameSpans []Span
	// IsFunction is set for "local function f", whose body sees f (unlike
	// that of "local f = function"). go-moonscript patch.
	IsFunction bool
}

type FuncCallStmt struct {
	StmtBase

	Expr Expr
}

type DoBlockStmt struct {
	StmtBase

	Stmts     []Stmt
	StmtsSpan Span // go-moonscript patch: the span of the block
}

type WhileStmt struct {
	StmtBase

	Condition Expr
	Stmts     []Stmt
	StmtsSpan Span // go-moonscript patch: the span of the block
}

type RepeatStmt struct {
	StmtBase

	Condition Expr
	Stmts     []Stmt
	StmtsSpan Span // go-moonscript patch: the span of the block
}

// IfStmt is an if statement. An "elseif" is an IfStmt alone in the Else
// of the one before, spanning from the "elseif" to the last token before
// the "end" (go-moonscript patch: the spans).
type IfStmt struct {
	StmtBase

	Condition Expr
	Then      []Stmt
	Else      []Stmt
	// ThenSpan is the span of the block Then; ElseSpan that of the block
	// after the "else" (the zero Span without one), or for an "elseif" that of
	// its IfStmt (go-moonscript patch).
	ThenSpan Span
	ElseSpan Span
}

type NumberForStmt struct {
	StmtBase

	Name  string
	Init  Expr
	Limit Expr
	Step  Expr
	Stmts []Stmt
	// go-moonscript patch: the spans of Name and of the block
	NameSpan  Span
	StmtsSpan Span
}

type GenericForStmt struct {
	StmtBase

	Names []string
	Exprs []Expr
	Stmts []Stmt
	// go-moonscript patch: the spans of Names and of the block
	NameSpans []Span
	StmtsSpan Span
}

type FuncDefStmt struct {
	StmtBase

	Name *FuncName
	Func *FunctionExpr
}

type ReturnStmt struct {
	StmtBase

	Exprs []Expr
}

type BreakStmt struct {
	StmtBase
}

type LabelStmt struct {
	StmtBase

	Name     string
	NameSpan Span // go-moonscript patch
}

type GotoStmt struct {
	StmtBase

	Label     string
	LabelSpan Span // go-moonscript patch
}
