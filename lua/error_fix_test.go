package lua

// go-moonscript patch: Go API tests of Lua 5.1's runtime error messages
// (ldebug.go); the Lua side is tested by testdata/compat/vm_error_*.lua.

import (
	"strings"
	"testing"
)

// errorOf runs code and returns the message of the error it raises.
func errorOf(t *testing.T, L *LState, code string) string {
	t.Helper()
	err := L.DoString(code)
	if err == nil {
		t.Fatalf("%s: no error", code)
	}
	return err.(*ApiError).Object.String()
}

// Under DialectLuaJIT, comparisons of different types are reported with
// both type names ("string" and "thread" have the same third letter, which
// is what Lua 5.1 compares).
func TestErrorCompareDialect(t *testing.T) {
	const code = `local co = coroutine.create(function() end) return "a" < co`
	for _, c := range []struct {
		dialect Dialect
		want    string
	}{
		{DialectLua51, "<string>:1: attempt to compare two string values"},
		{DialectLuaJIT, "<string>:1: attempt to compare string with thread"},
	} {
		L := NewState(Options{Dialect: c.dialect})
		if got := errorOf(t, L, code); got != c.want {
			t.Errorf("%v: %q, want %q", c.dialect, got, c.want)
		}
		L.Close()
	}
}

// Argument errors of Go functions name them as the call does, and the
// errors of the Go API (L.GetField on a non-table) name no variable.
func TestErrorGoFunctions(t *testing.T) {
	L := NewState()
	defer L.Close()
	L.SetGlobal("gofn", L.NewFunction(func(L *LState) int {
		L.CheckInt(2)
		return 0
	}))
	L.SetGlobal("getfield", L.NewFunction(func(L *LState) int {
		L.Push(L.GetField(L.Get(1), "x"))
		return 1
	}))
	L.SetGlobal("raise", L.NewFunction(func(L *LState) int {
		L.Error(L.Get(1), L.OptInt(2, 1))
		return 0
	}))
	for _, c := range []struct{ code, want string }{
		{`gofn(1)`, "<string>:1: bad argument #2 to 'gofn' (number expected, got no value)"},
		{`local t = {f = gofn} t.f(1, {})`, "<string>:1: bad argument #2 to 'f' (number expected, got table)"},
		{`local t = {m = gofn} t:m()`, "<string>:1: bad argument #1 to 'm' (number expected, got no value)"},
		{`local t = {m = gofn} t.m(t, "x")`, "<string>:1: bad argument #2 to 'm' (number expected, got string)"},
		{`local ok, e = pcall(gofn, 1) error(e, 0)`, "bad argument #2 to '?' (number expected, got no value)"},
		{`local t getfield(t)`, "<string>:1: attempt to index a nil value"},
		{`raise("msg")`, "<string>:1: msg"},
		{`local function f() raise("msg", 2) end
f()`, "<string>:2: msg"},
		{`raise("msg", 0)`, "msg"},
		{`raise("msg", 9)`, "msg"},
	} {
		if got := errorOf(t, L, c.code); got != c.want {
			t.Errorf("%s: %q, want %q", c.code, got, c.want)
		}
	}
}

// GetInfo's 'n' gives the name and namewhat of Lua 5.1's getfuncname.
func TestErrorGetInfoNameWhat(t *testing.T) {
	L := NewState()
	defer L.Close()
	var got []string
	L.SetGlobal("probe", L.NewFunction(func(L *LState) int {
		dbg, ok := L.GetStack(1)
		if !ok {
			t.Fatal("no level 1")
		}
		if _, err := L.GetInfo("n", dbg, LNil); err != nil {
			t.Fatal(err)
		}
		got = append(got, dbg.NameWhat+" "+dbg.Name)
		return 0
	}))
	if err := L.DoString(`
		local function lf() probe() end
		lf()
		local t = {f = function() probe() end}
		t.f()
		function t:m() probe() end
		t:m()
		function gf() probe() end
		gf()
		pcall(gf)
	`); err != nil {
		t.Fatal(err)
	}
	want := "local lf|field f|method m|global gf| "
	if s := strings.Join(got, "|"); s != want {
		t.Errorf("got %q, want %q", s, want)
	}
}

// Under DialectLuaJIT, debug.getinfo names a metamethod called by an
// instruction as LuaJIT 2.1 does (namewhat "metamethod", the event); Lua
// 5.1 gives no name. The expected names are luajit's.
func TestErrorGetInfoMetamethod(t *testing.T) {
	const code = `
		local what = {}
		local function mm()
			local dbg = debug.getinfo(1, "n")
			what[#what + 1] = dbg.namewhat .. " " .. (dbg.name or "?")
		end
		local mt = {__index = mm, __newindex = mm, __eq = mm, __add = mm, __sub = mm,
			__mul = mm, __div = mm, __mod = mm, __pow = mm, __unm = mm, __len = mm,
			__lt = mm, __le = mm, __concat = mm, __call = mm}
		local t, t2 = setmetatable({}, mt), setmetatable({}, mt)
		local x = t.x; t.x = 1; x = t + t; x = t - t; x = t * 2; x = 1 / t
		x = t % t; x = t ^ t; x = -t; x = t .. t; x = t(); x = t == t2
		x = t < t2; x = t >= t2
		return table.concat(what, "|")`
	for _, c := range []struct {
		dialect Dialect
		want    string
	}{
		{DialectLua51, " ?| ?| ?| ?| ?| ?| ?| ?| ?| ?|local t| ?| ?| ?"},
		{DialectLuaJIT, "metamethod __index|metamethod __newindex|metamethod __add|" +
			"metamethod __sub|metamethod __mul|metamethod __div|metamethod __mod|" +
			"metamethod __pow|metamethod __unm|metamethod __concat|local t|" +
			"metamethod __eq|metamethod __lt|metamethod __le"},
	} {
		L := NewState(Options{Dialect: c.dialect})
		if err := L.DoString(code); err != nil {
			t.Fatal(err)
		}
		if got := L.Get(-1).String(); got != c.want {
			t.Errorf("%v: %q, want %q", c.dialect, got, c.want)
		}
		L.Close()
	}
}
