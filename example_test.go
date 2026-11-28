package moonscript_test

import (
	"fmt"

	moonscript "github.com/tarantool-contrib/go-moonscript"
	"github.com/tarantool-contrib/go-moonscript/lint"
	lua "github.com/tarantool-contrib/go-moonscript/lua"
	"github.com/tarantool-contrib/go-moonscript/runtime"
)

func ExampleToLua() {
	code, _, err := moonscript.ToLua(`greet = (name) -> print "hello #{name}"`, moonscript.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Print(code)
	// Output:
	// local greet
	// greet = function(name)
	//   return print("hello " .. tostring(name))
	// end
}

func ExampleToLua_error() {
	_, _, err := moonscript.ToLua("x = = 1", moonscript.Options{})
	fmt.Println(err)
	// Output:
	// failed to parse at line 1, column 5:
	//   x = = 1
	//       ^
}

func Example_runtime() {
	L := runtime.NewState()
	defer L.Close()

	fn, err := runtime.LoadString(L, `
class Counter
  new: => @n = 0
  inc: => @n += 1

c = Counter!
c\inc! for _ = 1, 3
print "count", c.n
`, "=example", runtime.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}
	L.Push(fn)
	if err := L.PCall(0, 0, nil); err != nil {
		fmt.Println(err)
	}
	// Output:
	// count	3
}

// Calling Go from MoonScript, and requiring .moon modules.
func Example_require() {
	L := runtime.NewState()
	defer L.Close()

	// A Go function, as a global.
	L.SetGlobal("greet", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString("hello, " + L.CheckString(1)))
		return 1
	}))

	// The .moon loader: require finds modules along package.moonpath
	// (by default derived from package.path: ./?.lua -> ./?.moon).
	if _, err := runtime.InsertLoader(L); err != nil {
		fmt.Println(err)
		return
	}
	if err := L.DoString(`package.moonpath = "testdata/example/?.moon"`); err != nil {
		fmt.Println(err)
		return
	}

	fn, err := runtime.LoadString(L, `
util = require "lib.util"
print greet("moon"), util.double 21
`, "=example", runtime.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}
	L.Push(fn)
	if err := L.PCall(0, 0, nil); err != nil {
		fmt.Println(err)
	}
	// Output:
	// hello, moon	42
}

func Example_lint() {
	report, found, err := lint.Code("x = 1\nprint y\n", "input.moon", nil, lint.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}
	if found {
		fmt.Print(report)
	}
	// Output:
	// input.moon
	//
	// line 2: accessing global `y`
	// ============================
	// > print y
	//
	// line 1: assigned but unused `x`
	// ===============================
	// > x = 1
}
