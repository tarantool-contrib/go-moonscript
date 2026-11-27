package runtime

// The LuaJIT 2.1 dialect of the compatibility layer (a state created with
// lua.Options{Dialect: lua.DialectLuaJIT}, see NewState): numbers convert
// to strings as LuaJIT does (lua.JITNumberString); string.format is
// gopher-lua's LuaJIT one, %q included. LuaJIT's load, loadstring and
// loadfile, with Lua chunk names, are gopher-lua's own.

import (
	lua "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua"
)

// numberString converts a number to a string as tostring does in the
// dialect of L.
func numberString(L *lua.LState, f float64) string {
	if L.Dialect() == lua.DialectLuaJIT {
		return lua.JITNumberString(f)
	}
	return formatNumber(f)
}
