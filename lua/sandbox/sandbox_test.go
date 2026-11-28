package sandbox_test

import (
	"strings"
	"testing"

	lua "github.com/tarantool-contrib/go-moonscript/lua"
	"github.com/tarantool-contrib/go-moonscript/lua/sandbox"
)

// run loads code into an environment built from rules and returns what the
// code returns, as tostring'd values joined by spaces, or the error.
func run(t *testing.T, L *lua.LState, rules sandbox.Rules, code string) (string, error) {
	t.Helper()
	env, err := sandbox.Env(L, rules)
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	fn, err := sandbox.Load(L, env, code, "sandboxed")
	if err != nil {
		return "", err
	}
	top := L.GetTop()
	L.Push(fn)
	if err := L.PCall(0, lua.MultRet, nil); err != nil {
		return "", err
	}
	var out []string
	for i := top + 1; i <= L.GetTop(); i++ {
		out = append(out, L.ToStringMeta(L.Get(i)).String())
	}
	L.SetTop(top)
	return strings.Join(out, " "), nil
}

func TestEnv(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	for _, c := range []struct {
		name  string
		rules sandbox.Rules
		code  string
		want  string
	}{
		{"base and string", sandbox.Rules{Globals: []string{"*", "string.*"}},
			`return string.format("%d", 42), type(pairs), _G == getfenv, _VERSION`, "42 function false Lua 5.1"},
		{"no other globals", sandbox.Rules{Globals: []string{"*"}},
			`return type(os), type(io), type(string), type(debug), type(require)`, "nil nil nil nil nil"},
		{"masks skip risky", sandbox.Rules{Globals: []string{"*", "os.*", "string.*"}},
			`return type(loadstring), type(setfenv), type(collectgarbage), type(os.time), type(os.execute), type(os.getenv), type(string.dump)`,
			"nil nil nil function nil nil nil"},
		{"risky by name", sandbox.Rules{Globals: []string{"os.getenv", "type"}},
			`return type(os.getenv), type(os.time)`, "function nil"},
		{"_G is the environment", sandbox.Rules{Globals: []string{"type"}},
			`x = 1; return _G.x, _G._G == _G`, "1 true"},
		{"library tables are frozen copies", sandbox.Rules{Globals: []string{"pcall", "type", "string.*"}},
			`local ok, err = pcall(function() string.format = nil end); return ok, err, type(string.format)`,
			"false sandboxed:1: attempt to modify a read-only table function"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := run(t, L, c.rules, c.code)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
	if err := L.DoString(`assert(type(string.format) == "function" and x == nil)`); err != nil {
		t.Errorf("the host's globals changed: %v", err)
	}
}

func TestEnvErrors(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	for _, c := range []struct {
		pattern, want string
	}{
		{"nosuch", `no global "nosuch"`},
		{"string", `"string" is a table`},
		{"nolib.x", `no library "nolib"`},
		{"string.nosuch", `no "string.nosuch"`},
		{"io.*", `"io.*" matches nothing`},
	} {
		_, err := sandbox.Env(L, sandbox.Rules{Globals: []string{c.pattern}})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error with %q", c.pattern, err, c.want)
		}
	}
}

func TestLoadText(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	if err := L.DoString(`bin = string.dump(function() return 1 end)`); err != nil {
		t.Fatal(err)
	}
	bin := string(L.GetGlobal("bin").(lua.LString))
	env, err := sandbox.Env(L, sandbox.Rules{Globals: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.Load(L, env, bin, "bin"); err == nil || !strings.Contains(err.Error(), "binary chunk") {
		t.Errorf("Load of a binary chunk: %v", err)
	}

	rules := sandbox.Rules{Globals: []string{"loadstring", "load", "type", "os.time"}}
	env, err = sandbox.Env(L, rules)
	if err != nil {
		t.Fatal(err)
	}
	env.RawSetString("bin", lua.LString(bin))
	fn, err := sandbox.Load(L, env, `
local f = loadstring("return type(os), type(os.execute), type(print)")
local pieces = {"return ", "7"}
local i = 0
local g = load(function() i = i + 1; return pieces[i] end)
local b, msg = loadstring(bin)
local t1, t2, t3 = f()
return t1, t2, t3, g(), b, msg`, "loader")
	if err != nil {
		t.Fatal(err)
	}
	L.Push(fn)
	if err := L.PCall(0, 6, nil); err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := -6; i <= -1; i++ {
		out = append(out, L.Get(i).String())
	}
	// the loaded chunks run in the environment: os is there, print is not
	if got, want := strings.Join(out, " "), "table nil nil 7 nil attempt to load a binary chunk"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRequire(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	if err := L.DoString(`
package.preload["json"] = function() return {encode = function() return "{}" end} end
package.preload["lib.a"] = function() return "a" end
package.preload["other"] = function() return "other" end`); err != nil {
		t.Fatal(err)
	}
	rules := sandbox.Rules{Globals: []string{"pcall"}, Modules: []string{"json", "lib.*"}}
	got, err := run(t, L, rules, `
local ok, e = pcall(require, "other")
return require("json").encode(), require("lib.a"), ok, e`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{} a false module 'other' is not allowed"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEnvLuaJIT(t *testing.T) {
	L := lua.NewState(lua.Options{Dialect: lua.DialectLuaJIT})
	defer L.Close()
	got, err := run(t, L, sandbox.Rules{Globals: []string{"*", "bit.*"}},
		`return bit.band(6, 3), type(jit), type(load), type(os)`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "2 nil nil nil"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := sandbox.Env(L, sandbox.Rules{Globals: []string{"jit.*"}}); err == nil {
		t.Error(`"jit.*" matched risky functions`)
	}
}

// Sandboxed code cannot change the library tables of its environment, by
// any means, in either dialect; it can still set globals.
func TestEnvFrozenLibraries(t *testing.T) {
	for _, dialect := range []lua.Dialect{lua.DialectLua51, lua.DialectLuaJIT} {
		L := lua.NewState(lua.Options{Dialect: dialect})
		globals := []string{"*", "string.*", "table.*", "math.*", "os.time", "os.clock", "coroutine.*"}
		libs := []string{"string", "table", "math", "os", "coroutine"}
		if dialect == lua.DialectLuaJIT {
			globals = append(globals, "bit.*")
			libs = append(libs, "bit")
		}
		rules := sandbox.Rules{Globals: globals}
		for _, lib := range libs {
			for _, code := range []string{
				lib + ".x = 1",
				lib + "[1] = 1",
				"for k in pairs(" + lib + ") do " + lib + "[k] = nil end",
				"rawset(" + lib + ", 'x', 1)",
				"setmetatable(" + lib + ", {})",
				"table.insert(" + lib + ", 1)",
				"table.remove(" + lib + ")",
				"table.sort(" + lib + ")",
			} {
				got, err := run(t, L, rules, "local ok, err = pcall(function() "+code+" end) return ok, err")
				if err != nil {
					t.Fatal(err)
				}
				if want := "false sandboxed:1: attempt to modify a read-only table"; got != want {
					t.Errorf("%s: got %q, want %q", code, got, want)
				}
			}
		}
		got, err := run(t, L, rules, `x = 1; string = "mine"; return x, string, _G.string`)
		if err != nil {
			t.Fatal(err)
		}
		if want := "1 mine mine"; got != want {
			t.Errorf("globals: got %q, want %q", got, want)
		}
		if err := L.DoString(`assert(type(string.format) == "function" and string.x == nil and x == nil)
			assert(getmetatable(string) == nil and table.x == nil)
			string.x = 1 -- the host's tables are not frozen
			assert(string.x == 1)`); err != nil {
			t.Errorf("the host's libraries: %v", err)
		}
		L.Close()
	}
}

// Without FreezeLibraries, sandboxed code reaches the state's string table
// through the string metatable; with it, it cannot change it, nor the
// other libraries of the state, which still work.
func TestFreezeLibraries(t *testing.T) {
	for _, dialect := range []lua.Dialect{lua.DialectLua51, lua.DialectLuaJIT} {
		L := lua.NewState(lua.Options{Dialect: dialect})
		rules := sandbox.Rules{Globals: []string{"*"}}
		got, err := run(t, L, rules, `return pcall(function() getmetatable("").__index.upper = function() return "patched" end end)`)
		if err != nil {
			t.Fatal(err)
		}
		if got != "true" {
			t.Fatalf("without FreezeLibraries: got %q", got)
		}
		if err := L.DoString(`assert(("x"):upper() == "patched")`); err != nil {
			t.Fatalf("without FreezeLibraries: %v", err)
		}
		L.Close()

		L = lua.NewState(lua.Options{Dialect: dialect})
		if err := L.DoString(`package.preload.mod = function(...) return {name = ...} end`); err != nil {
			t.Fatal(err)
		}
		sandbox.FreezeLibraries(L)
		for _, code := range []string{
			`getmetatable("").__index.upper = nil`,
			`getmetatable("").__index = {}`,
			`getmetatable("").__call = print`,
			`rawset(getmetatable(""), "__index", {})`,
			`setmetatable(getmetatable(""), {})`,
		} {
			got, err := run(t, L, rules, "return pcall(function() "+code+" end)")
			if err != nil {
				t.Fatal(err)
			}
			if want := "false sandboxed:1: attempt to modify a read-only table"; got != want {
				t.Errorf("%s: got %q, want %q", code, got, want)
			}
		}
		// debug.setmetatable cannot replace the metatable of strings (or of
		// another type that shares one), from the host either
		if err := L.DoString(`
			for _, v in ipairs{"", 1, true, print} do
				local ok, err = pcall(debug.setmetatable, v, {})
				assert(not ok and err == "cannot change a protected metatable", tostring(err))
			end
			assert(not pcall(debug.setmetatable, nil, {}))
			assert(getmetatable("").__index == string)
			local t = {}
			debug.setmetatable(t, {__index = {k = 1}})
			assert(t.k == 1, "tables keep their own metatables")
		`); err != nil {
			t.Errorf("debug.setmetatable after FreezeLibraries: %v", err)
		}
		// the host cannot change them either, and the libraries work
		if err := L.DoString(`
			for _, lib in ipairs{"string", "table", "math", "os", "io", "coroutine", "debug"} do
				local ok, err = pcall(function() _G[lib].x = 1 end)
				assert(not ok and err:find("read-only", 1, true), lib)
			end
			assert(not pcall(function() getmetatable(io.stdout).write = nil end))
			assert(not pcall(rawset, getmetatable(io.stdout), "x", 1))
			assert(("abc"):upper() == "ABC" and string.format("%5.1f", 1.5) == "  1.5")
			assert(("a,b"):gsub(",", ";") == "a;b" and select(2, ("aaa"):gsub("a", "b")) == 3)
			assert(("x"):rep(3) == "xxx" and ("hello"):find("l") == 3)
			for w in ("a b"):gmatch("%a") do end
			local t = {3, 1, 2}
			table.sort(t) table.insert(t, 4) table.remove(t, 1)
			assert(table.concat(t, ",") == "2,3,4")
			math.randomseed(42) assert(math.random(10) >= 1 and math.floor(math.pi) == 3)
			assert(type(os.time()) == "number" and type(os.date("*t")) == "table")
			local co = coroutine.wrap(function(a) local b = coroutine.yield(a + 1) return b * 2 end)
			assert(co(1) == 2 and co(5) == 10)
			assert(type(debug.traceback()) == "string" and debug.getinfo(1, "S").what == "main")
			assert(require("mod").name == "mod" and package.loaded.mod)
			package.path = "./?.lua"
			local f = io.tmpfile() f:write("data") f:seek("set") assert(f:read("*a") == "data") f:close()
			x = 1
			assert(x == 1)
		`); err != nil {
			t.Errorf("libraries after FreezeLibraries: %v", err)
		}
		if dialect == lua.DialectLuaJIT {
			if err := L.DoString(`
				assert(bit.band(6, 3) == 2 and type(jit.version) == "string")
				assert(not pcall(function() bit.x = 1 end) and not pcall(function() jit.x = 1 end))
				local new, clear = require("table.new"), require("table.clear")
				local t = new(4, 0) t[1] = 1 clear(t) assert(next(t) == nil)
				t = table.move({1, 2}, 1, 2, 1, {}) assert(t[2] == 2)
			`); err != nil {
				t.Errorf("LuaJIT libraries after FreezeLibraries: %v", err)
			}
		}
		L.Close()
	}
}
