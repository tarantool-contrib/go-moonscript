package runtime

// The LuaJIT 2.1 dialect of the compatibility layer (a state created with
// lua.Options{Dialect: lua.DialectLuaJIT}, see NewState): loadfile takes
// LuaJIT's arguments (load and loadstring are gopher-lua's); numbers
// convert to strings as LuaJIT does (lua.JITNumberString); string.format
// is gopher-lua's LuaJIT one, %q included.

import (
	"io"
	"os"

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

// openLuaJITCompat replaces the loadfile of OpenCompat with LuaJIT 2.1's.
func openLuaJITCompat(L *lua.LState) {
	L.G.Global.RawSetString("loadfile", L.NewFunction(jitLoadFile))
}

func optString(L *lua.LState, n int) (string, bool) {
	if L.Get(n) == lua.LNil {
		return "", false
	}
	return L.CheckString(n), true
}

// loadChecked loads code as load does after reading it: mode and
// precompiled chunks checked, the environment set.
func loadChecked(L *lua.LState, code, chunkname, mode string, hasMode bool, env *lua.LTable) int {
	if msg := lua.JITLoadModeError(code, mode, hasMode); msg != "" {
		L.Push(lua.LNil)
		L.Push(lua.LString(msg))
		return 2
	}
	if lua.JITIsBinaryChunk(code) && !lua.IsDump(code) { // this VM's own dumps load
		L.Push(lua.LNil)
		L.Push(lua.LString(lua.JITBytecodeError(chunkname)))
		return 2
	}
	n := load(L, code, chunkname)
	if fn, ok := L.Get(-1).(*lua.LFunction); ok && n == 1 && env != nil {
		fn.Env = env
	}
	return n
}

// jitLoadFile is loadfile([filename [, mode [, env]]]).
func jitLoadFile(L *lua.LState) int {
	path, _ := optString(L, 1)
	mode, hasMode := optString(L, 2)
	env, _ := L.Get(3).(*lua.LTable)
	var code, chunkname string
	if path == "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString("cannot read stdin: " + strerror(err)))
			return 2
		}
		code, chunkname = string(data), "=stdin"
	} else {
		text, err := readChunkFile(L, path)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		code, chunkname = text, "@"+path
	}
	return loadChecked(L, code, chunkname, mode, hasMode, env)
}
