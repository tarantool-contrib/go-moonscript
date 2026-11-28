package lua

// go-moonscript patch: tests of LState.FreezeTypeMetatables.

import (
	"strings"
	"testing"
)

func TestFreezeTypeMetatables(t *testing.T) {
	for _, dialect := range []Dialect{DialectLua51, DialectLuaJIT} {
		L := NewState(Options{Dialect: dialect})
		// by default, as in Lua, they can be set
		if err := L.DoString(`
			debug.setmetatable(1, {__index = {double = function(n) return n * 2 end}})
			assert((2):double() == 4)
			debug.setmetatable(1, nil)
		`); err != nil {
			t.Fatalf("%v: before: %v", dialect, err)
		}
		if L.TypeMetatablesFrozen() {
			t.Fatalf("%v: frozen by default", dialect)
		}
		L.FreezeTypeMetatables()
		if !L.TypeMetatablesFrozen() {
			t.Fatalf("%v: not frozen", dialect)
		}
		if err := L.DoString(`
			for _, v in ipairs{"", 1, true, print, coroutine.create(function() end)} do
				local ok, err = pcall(debug.setmetatable, v, {})
				assert(not ok and err == "cannot change a protected metatable", type(v) .. ": " .. tostring(err))
				ok, err = pcall(debug.setmetatable, v, nil)
				assert(not ok, type(v) .. ": nil metatable set")
			end
			assert(getmetatable("").__index == string and getmetatable(1) == nil)
			local mt = {}
			assert(getmetatable(setmetatable({}, mt)) == mt)
			local u = {} debug.setmetatable(u, mt) assert(getmetatable(u) == mt)
			-- in a coroutine, which shares the state's metatables
			local co = coroutine.create(function() return pcall(debug.setmetatable, "", {}) end)
			local _, ok = coroutine.resume(co)
			assert(ok == false)
		`); err != nil {
			t.Errorf("%v: after: %v", dialect, err)
		}
		// from Go: the Lua error
		fn := L.NewFunction(func(L *LState) int {
			L.SetMetatable(LString(""), L.NewTable())
			return 0
		})
		L.Push(fn)
		if err := L.PCall(0, 0, nil); err == nil || !strings.Contains(err.Error(), "cannot change a protected metatable") {
			t.Errorf("%v: SetMetatable from Go: %v", dialect, err)
		}
		L.Close()
	}
}
