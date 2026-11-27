package ast

type Field struct {
	Key   Expr
	Value Expr
}

type ParList struct {
	HasVargs bool
	// NoArg: a vararg function without Lua 5.1's implicit local "arg"
	// (LUA_COMPAT_VARARG), as in LuaJIT (go-moonscript patch)
	NoArg bool
	Names []string
}

type FuncName struct {
	Func     Expr
	Receiver Expr
	Method   string
}
