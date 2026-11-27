package sandbox_test

import (
	"strings"
	"testing"

	lua "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/sandbox"
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
		{"library tables are copies", sandbox.Rules{Globals: []string{"string.*"}},
			`string.format = nil; return string.format`, "nil"},
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
