package runtime

// The LuaJIT 2.1 dialect of the compatibility layer (a state created with
// lua.Options{Dialect: lua.DialectLuaJIT}, see NewState): load,
// loadstring (which does the same) and loadfile take LuaJIT's arguments,
// with this package's chunk naming (ChunkID) and syntax errors; numbers
// convert to strings as LuaJIT does (lua.JITNumberString); string.format
// is gopher-lua's LuaJIT one, %q included.

import (
	"errors"
	"io"
	"os"
	"strings"

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

// openLuaJITCompat replaces the load functions of OpenCompat with
// LuaJIT 2.1's.
func openLuaJITCompat(L *lua.LState) {
	g := L.G.Global
	g.RawSetString("load", L.NewFunction(jitLoad))
	g.RawSetString("loadstring", L.NewFunction(jitLoad)) // the same, another function
	g.RawSetString("loadfile", L.NewFunction(jitLoadFile))
}

func optString(L *lua.LState, n int) (string, bool) {
	if L.Get(n) == lua.LNil {
		return "", false
	}
	return L.CheckString(n), true
}

// jitLoad is load(chunk [, chunkname [, mode [, env]]]): the chunk is a
// string (or number), named by itself by default, or a reader function
// ("=(load)"); a mode without 't' rejects source chunks; a table env is
// the environment of the loaded function.
func jitLoad(L *lua.LState) int {
	chunkname, hasName := optString(L, 2)
	mode, hasMode := optString(L, 3)
	env, _ := L.Get(4).(*lua.LTable)
	var code string
	if lv := L.Get(1); lv.Type() == lua.LTString || lv.Type() == lua.LTNumber {
		code = L.CheckString(1)
		if !hasName {
			chunkname = code
		}
	} else {
		fn := L.CheckFunction(1)
		if !hasName {
			chunkname = "=(load)"
		}
		top := L.GetTop()
		var buf strings.Builder
		for {
			L.SetTop(top)
			L.Push(fn)
			if err := L.PCall(0, 1, nil); err != nil {
				L.SetTop(top)
				L.Push(lua.LNil)
				if ae := (*lua.ApiError)(nil); errors.As(err, &ae) {
					L.Push(ae.Object)
				} else {
					L.Push(lua.LString(err.Error()))
				}
				return 2
			}
			piece := L.Get(-1)
			if piece == lua.LNil {
				break
			}
			if piece.Type() != lua.LTString && piece.Type() != lua.LTNumber {
				L.SetTop(top)
				L.Push(lua.LNil)
				L.Push(lua.LString("reader function must return a string"))
				return 2
			}
			s := L.ToString(-1)
			if s == "" {
				break
			}
			buf.WriteString(s)
		}
		L.SetTop(top)
		code = buf.String()
	}
	return loadChecked(L, code, chunkname, mode, hasMode, env)
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
		text, err := readChunkFile(path)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		code, chunkname = text, "@"+path
	}
	return loadChecked(L, code, chunkname, mode, hasMode, env)
}
