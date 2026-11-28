package lua_test

// go-moonscript patch: examples of CompileAST and LState.LoadAST
// (compileast.go).

import (
	"fmt"
	"reflect"

	lua "github.com/tarantool-contrib/go-moonscript/lua"
	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
)

// renameVariable renames the variable old to new everywhere in a tree: the
// names of package ast's IdentExpr nodes, which hold the names of
// variables (old must not be the name of a local of the chunk, for a
// global's renaming).
func renameVariable(node any, old, new string) {
	v := reflect.ValueOf(node)
	switch v.Kind() {
	case reflect.Slice:
		for i := range v.Len() {
			renameVariable(v.Index(i).Interface(), old, new)
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		if id, ok := node.(*ast.IdentExpr); ok {
			if id.Value == old {
				id.Value = new
			}
			return
		}
		if s := v.Elem(); s.Kind() == reflect.Struct {
			for i := range s.NumField() {
				if f := s.Field(i); f.CanInterface() {
					renameVariable(f.Interface(), old, new)
				}
			}
		}
	}
}

// A chunk is parsed, its tree transformed (the global "say" renamed "log"),
// and the tree loaded and run, without printing it back to Lua source.
// The error of the call shows the line of the source.
func ExampleLState_LoadAST() {
	src := `local greeting = "hello, " .. name
say(greeting)
say(greeting:upper())
local ok, err = pcall(function()
  return greeting.missing.field
end)
say(err)`
	chunk, err := parse.ParseBytes([]byte(src), "greet.lua", parse.Lua51)
	if err != nil {
		panic(err)
	}
	renameVariable(chunk, "say", "log")

	L := lua.NewState()
	defer L.Close()
	L.SetGlobal("name", lua.LString("world"))
	L.SetGlobal("log", L.NewFunction(func(L *lua.LState) int {
		fmt.Println(L.CheckString(1))
		return 0
	}))
	fn, err := L.LoadAST(chunk, "@greet.lua")
	if err != nil {
		panic(err)
	}
	L.Push(fn)
	if err := L.PCall(0, 0, nil); err != nil {
		panic(err)
	}
	// Output:
	// hello, world
	// HELLO, WORLD
	// greet.lua:5: attempt to index field 'missing' (a nil value)
}

// A chunk built by hand, without lines (it compiles as if on one line),
// that takes two parameters: gopher-lua's PR #402 (CompileWithParNames) is
// a "local a, b = ..." first, since the main function is vararg.
func ExampleCompileAST() {
	chunk := []ast.Stmt{
		&ast.LocalAssignStmt{Names: []string{"a", "b"}, Exprs: []ast.Expr{&ast.Comma3Expr{}}},
		&ast.ReturnStmt{Exprs: []ast.Expr{&ast.ArithmeticOpExpr{
			Operator: "*", Lhs: &ast.IdentExpr{Value: "a"}, Rhs: &ast.IdentExpr{Value: "b"},
		}}},
	}
	proto, err := lua.CompileAST(chunk, "=product", lua.DialectLua51)
	if err != nil {
		panic(err)
	}
	L := lua.NewState()
	defer L.Close()
	L.Push(L.NewFunctionFromProto(proto))
	L.Push(lua.LNumber(6))
	L.Push(lua.LNumber(7))
	L.Call(2, 1)
	fmt.Println(L.Get(-1))

	// a tree the compiler cannot compile is an error at its line
	chunk = append(chunk, &ast.BreakStmt{})
	_, err = lua.CompileAST(chunk, "=product", lua.DialectLua51)
	fmt.Println(err)
	// Output:
	// 42
	// product:1: no loop to break
}
