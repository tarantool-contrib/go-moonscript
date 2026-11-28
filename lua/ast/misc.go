package ast

// Field is an item of a table constructor: Key is nil for a positional
// item, a StringExpr spanning the name for "name = value". Its span is the
// item's, without the separator after it (go-moonscript patch: the node).
type Field struct {
	SpanBase

	Key   Expr
	Value Expr
}

// ParList is the parameter list of a function; its span is from the "("
// to the ")" (go-moonscript patch: the node and the spans).
type ParList struct {
	SpanBase

	HasVargs bool
	// NoArg: a vararg function without Lua 5.1's implicit local "arg"
	// (LUA_COMPAT_VARARG), as in LuaJIT (go-moonscript patch)
	NoArg bool
	Names []string
	// NameSpans are the spans of Names, if known (go-moonscript patch)
	NameSpans []Span
	// VarargSpan is the span of the "..." (go-moonscript patch)
	VarargSpan Span
}

// FuncName is the name of a function statement: Func for "function a.b",
// Receiver and Method for "function a.b:m". Its span is from the first
// name to the last (go-moonscript patch: the node and the span).
type FuncName struct {
	SpanBase

	Func     Expr
	Receiver Expr
	Method   string
	// MethodSpan is the span of the name Method (go-moonscript patch)
	MethodSpan Span
}
