// Package sandbox builds an environment for untrusted Lua code from an
// allow-list of globals and modules, and loads chunks into it.
//
// This is a first, minimal version: it decides what the code can reach by
// name, and nothing more. It sets no limits (use the state's context,
// Options.MemoryLimit and CallStackSize), does not restrict the file system
// or the language, and shares the state with the host: getmetatable("")
// returns the state's string metatable, whose __index is the state's real
// string table. Give sandboxed code a state of its own. See the design issue
// for the full mechanism. (go-moonscript)
package sandbox

import (
	"fmt"
	"sort"
	"strings"

	lua "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua"
)

// Rules say what the environment holds.
type Rules struct {
	// Globals are the globals the code may use: "print" (a global),
	// "os.time" (a field of a library table), "string.*" (the safe fields
	// of a library table) or "*" (the safe base functions). A mask never
	// matches a function that can leave the environment or reach the host
	// (see Risky); such a function is allowed only by its full name.
	Globals []string
	// Modules are the modules require may load: "json" or "lib.*" (lib and
	// its submodules). With any, the environment gets a require that loads
	// only those, in the host's environment: modules are trusted code.
	Modules []string
}

// Risky lists the functions a mask does not match: they load code that
// would run outside the environment or with bytecode, reach the caller's
// environment, or reach the host (files, processes, the collector). Naming
// one in Rules.Globals allows it; load and loadstring are then wrapped to
// load text only, into the environment.
var Risky = map[string]bool{
	"load": true, "loadstring": true, "loadfile": true, "dofile": true,
	"getfenv": true, "setfenv": true, "require": true, "module": true,
	"collectgarbage": true, "gcinfo": true, "newproxy": true,
	"string.dump": true,
	"os.execute":  true, "os.exit": true, "os.remove": true, "os.rename": true,
	"os.tmpname": true, "os.getenv": true, "os.setlocale": true,
}

// riskyLibs are the libraries every field of which is risky.
var riskyLibs = map[string]bool{
	"io": true, "debug": true, "package": true, "jit": true, "ffi": true, "channel": true,
}

func isRisky(name string) bool {
	if Risky[name] {
		return true
	}
	lib, _, ok := strings.Cut(name, ".")
	return ok && riskyLibs[lib]
}

// Env returns a new environment table holding what rules allow, taken from
// the globals of L. Library tables are copied, so code changing env.string
// does not change the host's string table. env._G is env. A name that
// L's globals do not have, or a mask that matches nothing, is an error.
func Env(L *lua.LState, rules Rules) (*lua.LTable, error) {
	globals := L.G.Global
	env := L.NewTable()
	for _, pattern := range rules.Globals {
		n, err := add(L, env, globals, pattern)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, fmt.Errorf("sandbox: %q matches nothing", pattern)
		}
	}
	if len(rules.Modules) > 0 {
		req, ok := globals.RawGetString("require").(*lua.LFunction)
		if !ok {
			return nil, fmt.Errorf("sandbox: the state has no require for Modules")
		}
		env.RawSetString("require", allowRequire(L, req, rules.Modules))
	}
	for _, name := range []string{"load", "loadstring"} {
		if fn, ok := env.RawGetString(name).(*lua.LFunction); ok {
			env.RawSetString(name, textLoader(L, fn, env))
		}
	}
	env.RawSetString("_G", env)
	return env, nil
}

// add adds what pattern names to env and returns how many values it added.
func add(L *lua.LState, env, globals *lua.LTable, pattern string) (int, error) {
	if pattern == "*" {
		n := 0
		for _, k := range sortedKeys(globals) {
			if _, ok := globals.RawGetString(k).(*lua.LFunction); ok && !isRisky(k) {
				env.RawSetString(k, globals.RawGetString(k))
				n++
			}
		}
		if v := globals.RawGetString("_VERSION"); v != lua.LNil {
			env.RawSetString("_VERSION", v)
		}
		return n, nil
	}
	lib, field, dotted := strings.Cut(pattern, ".")
	if !dotted {
		v := globals.RawGetString(pattern)
		if v == lua.LNil {
			return 0, fmt.Errorf("sandbox: no global %q", pattern)
		}
		if _, isTable := v.(*lua.LTable); isTable {
			return 0, fmt.Errorf("sandbox: %q is a table: name its fields or %q", pattern, pattern+".*")
		}
		env.RawSetString(pattern, v)
		return 1, nil
	}
	src, ok := globals.RawGetString(lib).(*lua.LTable)
	if !ok {
		return 0, fmt.Errorf("sandbox: no library %q", lib)
	}
	dst, ok := env.RawGetString(lib).(*lua.LTable)
	if !ok {
		dst = L.NewTable()
		env.RawSetString(lib, dst)
	}
	if field == "*" {
		n := 0
		for _, k := range sortedKeys(src) {
			if !isRisky(lib + "." + k) {
				dst.RawSetString(k, src.RawGetString(k))
				n++
			}
		}
		return n, nil
	}
	v := src.RawGetString(field)
	if v == lua.LNil {
		return 0, fmt.Errorf("sandbox: no %q", pattern)
	}
	dst.RawSetString(field, v)
	return 1, nil
}

func sortedKeys(t *lua.LTable) []string {
	var keys []string
	t.ForEach(func(k, _ lua.LValue) {
		if s, ok := k.(lua.LString); ok {
			keys = append(keys, string(s))
		}
	})
	sort.Strings(keys)
	return keys
}

// allowRequire returns a require that loads only the modules of modules,
// with the state's own require.
func allowRequire(L *lua.LState, req *lua.LFunction, modules []string) *lua.LFunction {
	return L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		if !allowed(modules, name) {
			L.RaiseError("module '%s' is not allowed", name)
		}
		top := L.GetTop()
		L.Push(req)
		L.Push(lua.LString(name))
		L.Call(1, lua.MultRet)
		return L.GetTop() - top
	})
}

func allowed(modules []string, name string) bool {
	for _, m := range modules {
		if prefix, ok := strings.CutSuffix(m, ".*"); ok {
			if name == prefix || strings.HasPrefix(name, prefix+".") {
				return true
			}
		} else if name == m {
			return true
		}
	}
	return false
}

// textLoader wraps load or loadstring: the chunk must be text (a chunk
// function's pieces are read first), and the function loaded runs in env.
func textLoader(L *lua.LState, orig *lua.LFunction, env *lua.LTable) *lua.LFunction {
	return L.NewFunction(func(L *lua.LState) int {
		chunk, isString := L.Get(1).(lua.LString)
		reader, isReader := L.Get(1).(*lua.LFunction)
		if isReader {
			chunk = lua.LString(readPieces(L, reader))
		}
		if (isString || isReader) && lua.IsBinaryChunk(string(chunk)) {
			L.Push(lua.LNil)
			L.Push(lua.LString("attempt to load a binary chunk"))
			return 2
		}
		if isReader { // the pieces read, as one
			done := false
			L.Replace(1, L.NewFunction(func(L *lua.LState) int {
				if done {
					return 0
				}
				done = true
				L.Push(chunk)
				return 1
			}))
		}
		args := make([]lua.LValue, 0, L.GetTop())
		for i := 1; i <= L.GetTop(); i++ {
			args = append(args, L.Get(i))
		}
		top := L.GetTop()
		L.Push(orig)
		for _, a := range args {
			L.Push(a)
		}
		L.Call(len(args), lua.MultRet)
		if fn, ok := L.Get(top + 1).(*lua.LFunction); ok {
			L.SetFEnv(fn, env)
		}
		return L.GetTop() - top
	})
}

// readPieces calls a load chunk function until it returns nil or "", as
// load does, and returns the pieces joined.
func readPieces(L *lua.LState, fn *lua.LFunction) string {
	var b strings.Builder
	for {
		L.Push(fn)
		L.Call(0, 1)
		v := L.Get(-1)
		L.Pop(1)
		s, ok := v.(lua.LString)
		if v == lua.LNil || ok && s == "" {
			return b.String()
		}
		if !ok {
			L.RaiseError("reader function must return a string")
		}
		b.WriteString(string(s))
	}
}

// Load compiles src as text (a binary chunk is an error) and returns the
// function, running in env.
func Load(L *lua.LState, env *lua.LTable, src, name string) (*lua.LFunction, error) {
	if lua.IsBinaryChunk(src) {
		return nil, fmt.Errorf("sandbox: %s: attempt to load a binary chunk", name)
	}
	fn, err := L.Load(strings.NewReader(src), name)
	if err != nil {
		return nil, err
	}
	L.SetFEnv(fn, env)
	return fn, nil
}
