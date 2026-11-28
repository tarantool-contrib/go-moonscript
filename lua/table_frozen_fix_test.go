package lua

// go-moonscript patch: tests of frozen tables (LTable.Freeze, table.go).

import (
	"errors"
	"strings"
	"testing"
)

// tzState returns a state of the dialect with a global frozen table
// t = {1, 2, 3, x = 1}.
func tzState(t *testing.T, dialect Dialect) *LState {
	t.Helper()
	L := NewState(Options{Dialect: dialect})
	if err := L.DoString(`t = {1, 2, 3, x = 1}`); err != nil {
		t.Fatal(err)
	}
	L.GetGlobal("t").(*LTable).Freeze()
	return L
}

var tzDialects = []struct {
	name    string
	dialect Dialect
}{{"lua51", DialectLua51}, {"luajit", DialectLuaJIT}}

// Every way Lua has to change a table raises the error, catchable by
// pcall, at the position of the assignment or of the library call, and
// leaves the table as it was.
func TestFrozenTableLua(t *testing.T) {
	cases := []struct {
		name, code string
		jit        bool // DialectLuaJIT only
		lua51      bool // DialectLua51 only
		msg        string
		where      string // the position, if not line 3 of the chunk
	}{
		{name: "existing string key (SETTABLEKS)", code: `t.x = 2`},
		{name: "existing string key to nil", code: `t.x = nil`},
		{name: "new string key", code: `t.y = 1`},
		{name: "existing array key (SETTABLE fast path)", code: `t[1] = 10`},
		{name: "array key in a loop", code: `for i = 1, 3 do t[i] = i end`},
		{name: "array key to nil", code: `t[3] = nil`},
		{name: "new array key", code: `t[4] = 4`},
		{name: "far number key", code: `t[1000] = 1`},
		{name: "float key", code: `t[1.5] = 1`},
		{name: "boolean key", code: `t[true] = 1`},
		{name: "table key", code: `t[{}] = 1`},
		{name: "variable string key (SETTABLE)", code: `local k = "x" t[k] = 5`},
		{name: "variable string new key", code: `local k = "z" t[k] = 5`},
		{name: "method definition", code: `function t.f() end`},
		{name: "multiple assignment", code: `local a; a, t.x = 1, 2`},
		{name: "global of a frozen environment (SETGLOBAL)",
			code: `local f = loadstring("y = 1", "=f") setfenv(f, t) f()`, where: "f:1: "},
		{name: "through __newindex = frozen table",
			code: `local p = setmetatable({}, {__newindex = t}) p.q = 1`},
		{name: "through __newindex chain",
			code: `local p = setmetatable({}, {__newindex = setmetatable({}, {__newindex = t})}) p[1] = 1`},
		{name: "rawset", code: `rawset(t, "x", 2)`},
		{name: "rawset new key", code: `rawset(t, 10, 2)`},
		{name: "table.insert", code: `table.insert(t, 4)`},
		{name: "table.insert at a position", code: `table.insert(t, 1, 0)`},
		{name: "table.remove", code: `table.remove(t)`},
		{name: "table.remove at a position", code: `table.remove(t, 1)`},
		{name: "table.sort", code: `table.sort(t)`},
		{name: "table.sort with a comparator", code: `table.sort(t, function(a, b) return a > b end)`},
		{name: "table.foreach body", code: `table.foreach(t, function(k) t[k] = nil end)`},
		{name: "table.foreachi body", code: `table.foreachi(t, function(i) t[i] = 0 end)`},
		{name: "table.setn", code: `table.setn(t, 1)`, lua51: true, msg: "'setn' is obsolete"},
		{name: "setmetatable", code: `setmetatable(t, {})`},
		{name: "setmetatable nil", code: `setmetatable(t, nil)`},
		{name: "debug.setmetatable", code: `debug.setmetatable(t, {})`},
		{name: "table.clear", code: `require("table.clear")(t)`, jit: true},
		{name: "table.move into it", code: `table.move({7, 8}, 1, 2, 1, t)`, jit: true},
		{name: "table.move within it", code: `table.move(t, 1, 2, 2)`, jit: true},
		{name: "table.move of nothing", code: `table.move({}, 1, 0, 1, t)`, jit: true},
	}
	for _, d := range tzDialects {
		for _, c := range cases {
			if c.jit && d.dialect != DialectLuaJIT || c.lua51 && d.dialect != DialectLua51 {
				continue
			}
			t.Run(d.name+"/"+c.name, func(t *testing.T) {
				L := tzState(t, d.dialect)
				defer L.Close()
				msg := c.msg
				if msg == "" {
					msg = "attempt to modify a read-only table"
				}
				// the code on line 3 of the chunk
				src := "local ok, err = pcall(function()\n\n" + c.code + "\nend)\nreturn ok, err"
				if err := L.DoString(src); err != nil {
					t.Fatal(err)
				}
				ok, err := L.Get(-2), L.Get(-1)
				if ok != LFalse {
					t.Fatalf("no error")
				}
				where := c.where
				if where == "" {
					where = "<string>:3: "
				}
				if want := where + msg; err.String() != want {
					t.Errorf("error %q, want %q", err, want)
				}
				// unchanged
				if err := L.DoString(`return t[1], t[2], t[3], t[4], t.x, t.y, t[1000], getmetatable(t), next(t, "x")`); err != nil {
					t.Fatal(err)
				}
				var got []string
				for i := -9; i <= -1; i++ {
					got = append(got, L.Get(i).String())
				}
				if want := "1 2 3 nil 1 nil nil nil nil"; strings.Join(got, " ") != want {
					t.Errorf("table changed: %s, want %s", strings.Join(got, " "), want)
				}
			})
		}
	}
}

// A __newindex metamethod of the frozen table is not called: the
// assignment raises the error.
func TestFrozenTableNewIndex(t *testing.T) {
	L := NewState()
	defer L.Close()
	if err := L.DoString(`
		called = 0
		ft = setmetatable({}, {__newindex = function() called = called + 1 end})
	`); err != nil {
		t.Fatal(err)
	}
	L.GetGlobal("ft").(*LTable).Freeze()
	if err := L.DoString(`
		local ok, err = pcall(function() ft.a = 1 end)
		assert(not ok and err:find("attempt to modify a read-only table", 1, true), err)
		ok, err = pcall(function() ft[1] = 1 end)
		assert(not ok and err:find("attempt to modify a read-only table", 1, true), err)
		assert(called == 0)
	`); err != nil {
		t.Fatal(err)
	}
}

// pcall of a library function directly: no position (the caller of rawset
// is pcall), as luaL_error in Lua 5.1.
func TestFrozenTableErrorPosition(t *testing.T) {
	L := tzState(t, DialectLua51)
	defer L.Close()
	if err := L.DoString(`
		local ok, err = pcall(rawset, t, 1, 2)
		assert(err == "attempt to modify a read-only table", err)
		ok, err = pcall(setmetatable, t, nil)
		assert(err == "attempt to modify a read-only table", err)
		ok, err = pcall(table.insert, t, 1)
		assert(err == "attempt to modify a read-only table", err)
	`); err != nil {
		t.Fatal(err)
	}
	err := L.DoString("local a = 1\nt.x = 2")
	var apiErr *ApiError
	if !errors.As(err, &apiErr) {
		t.Fatalf("%v: not an *ApiError", err)
	}
	if got := apiErr.Object.String(); got != "<string>:2: attempt to modify a read-only table" {
		t.Errorf("error %q", got)
	}
}

// A frozen table is readable, iterable, usable as a metatable and an
// __index, and its weak entries are still cleared.
func TestFrozenTableRead(t *testing.T) {
	for _, d := range tzDialects {
		t.Run(d.name, func(t *testing.T) {
			L := tzState(t, d.dialect)
			defer L.Close()
			mt := L.NewTable()
			L.SetField(mt, "__index", L.NewFunction(func(L *LState) int {
				L.Push(LString("from __index " + L.CheckString(2)))
				return 1
			}))
			L.SetField(mt, "__len", L.NewFunction(func(L *LState) int { L.Push(LNumber(42)); return 1 }))
			mt.Freeze()
			L.SetGlobal("mt", mt)
			if err := L.DoString(`
				assert(t.x == 1 and t[2] == 2 and rawget(t, 3) == 3 and t.nothing == nil)
				assert(#t == 3 and select("#", unpack(t)) == 3)
				local n = 0
				for k, v in pairs(t) do n = n + 1 end
				assert(n == 4)
				n = 0
				for i, v in ipairs(t) do n = n + 1 end
				assert(n == 3)
				assert(next(t) == 1)
				assert(table.concat(t, ",") == "1,2,3")
				assert(table.maxn(t) == 3)
				-- as a metatable and as an __index
				local o = setmetatable({}, mt)
				assert(o.a == "from __index a")
				assert(getmetatable(o) == mt)
				o.b = 1 -- o is not frozen
				assert(rawget(o, "b") == 1)
				local p = setmetatable({}, {__index = t})
				assert(p.x == 1 and p[1] == 1)
				p.x = 5
				assert(p.x == 5 and t.x == 1)
				-- frozen table with a metatable
				assert(setmetatable({}, {__index = setmetatable({}, mt)}).q == "from __index q")
			`); err != nil {
				t.Fatal(err)
			}
			var keys []string
			for k := range L.GetGlobal("t").(*LTable).All() {
				keys = append(keys, k.String())
			}
			if strings.Join(keys, " ") != "1 2 3 x" {
				t.Errorf("All: %v", keys)
			}
		})
	}
}

func TestFrozenTableWeak(t *testing.T) {
	for _, d := range tzDialects {
		t.Run(d.name, func(t *testing.T) {
			L := NewState(Options{Dialect: d.dialect})
			defer L.Close()
			L.SetGlobal("freeze", L.NewFunction(func(L *LState) int {
				L.CheckTable(1).Freeze()
				return 0
			}))
			if err := L.DoString(`
				-- a frozen weak table loses its collected entries
				local keep = {}
				local w = setmetatable({keep, {}, a = {}, b = keep}, {__mode = "v"})
				freeze(w)
				collectgarbage()
				collectgarbage()
				assert(w[1] == keep and w[2] == nil and w.a == nil and w.b == keep)
				assert(not pcall(function() w.c = 1 end))
				-- a frozen weak-keyed table too
				local k = {}
				local wk = setmetatable({[k] = 1, [{}] = 2}, {__mode = "k"})
				freeze(wk)
				collectgarbage()
				local n = 0
				for key in pairs(wk) do n = n + 1; assert(key == k) end
				assert(n == 1)
				-- a frozen table is collected
				local reg = setmetatable({}, {__mode = "k"})
				do
					local f = {1, 2, x = {}}
					freeze(f)
					reg[f] = true
				end
				collectgarbage()
				collectgarbage()
				assert(next(reg) == nil)
			`); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// SETLIST cannot reach a frozen table from Lua: only Go code that takes
// the constructor's table from the stack can freeze it.
func TestFrozenTableSetList(t *testing.T) {
	L := NewState()
	defer L.Close()
	L.SetGlobal("freezestack", L.NewFunction(func(L *LState) int {
		for _, v := range L.reg.array[:L.reg.top] {
			if tb, ok := v.(*LTable); ok && tb.RawGetString("keep") == LNil {
				tb.Freeze()
			}
		}
		return 0
	}))
	if err := L.DoString(`
		local ok, err = pcall(function()
			local t = {1, 2, freezestack()}
		end)
		assert(not ok and err:find(":3: attempt to modify a read-only table", 1, true), err)
	`); err != nil {
		t.Fatal(err)
	}
}

func tzPanics(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if err, ok := r.(error); !ok || !errors.Is(err, ErrFrozenTable) {
			t.Errorf("%s: recovered %v, want ErrFrozenTable", name, r)
		}
	}()
	f()
}

// The LTable methods panic; the LState functions raise the Lua error.
func TestFrozenTableGo(t *testing.T) {
	tb := newLTable(0, 0)
	tb.RawSetInt(1, LNumber(1))
	tb.RawSetString("x", LNumber(1))
	if tb.Frozen() {
		t.Fatal("frozen before Freeze")
	}
	tb.Freeze()
	if !tb.Frozen() {
		t.Fatal("not frozen after Freeze")
	}
	tzPanics(t, "RawSet", func() { tb.RawSet(LString("x"), LNumber(2)) })
	tzPanics(t, "RawSet nil value", func() { tb.RawSet(LString("x"), LNil) })
	tzPanics(t, "RawSetInt", func() { tb.RawSetInt(1, LNumber(2)) })
	tzPanics(t, "RawSetInt new key", func() { tb.RawSetInt(5, LNumber(2)) })
	tzPanics(t, "RawSetString", func() { tb.RawSetString("x", LNumber(2)) })
	tzPanics(t, "RawSetH", func() { tb.RawSetH(LString("y"), LNumber(2)) })
	tzPanics(t, "Append", func() { tb.Append(LNumber(2)) })
	tzPanics(t, "Append nil", func() { tb.Append(LNil) })
	tzPanics(t, "Insert", func() { tb.Insert(1, LNumber(2)) })
	tzPanics(t, "Remove", func() { tb.Remove(1) })
	if tb.RawGetInt(1) != LNumber(1) || tb.RawGetString("x") != LNumber(1) || tb.Len() != 1 {
		t.Error("the table changed")
	}

	for _, d := range tzDialects {
		L := NewState(Options{Dialect: d.dialect})
		calls := map[string]func(L *LState){
			"SetField":     func(L *LState) { L.SetField(tb, "x", LNumber(2)) },
			"SetTable":     func(L *LState) { L.SetTable(tb, LNumber(1), LNumber(2)) },
			"RawSet":       func(L *LState) { L.RawSet(tb, LString("y"), LNumber(2)) },
			"RawSetInt":    func(L *LState) { L.RawSetInt(tb, 2, LNumber(2)) },
			"SetMetatable": func(L *LState) { L.SetMetatable(tb, L.NewTable()) },
			"SetGlobal": func(L *LState) {
				L.G.Global.Freeze()
				L.SetGlobal("y", LTrue)
			},
			"SetFuncs (panics)": func(L *LState) {
				L.SetFuncs(tb, map[string]LGFunction{"f": func(*LState) int { return 0 }})
			},
		}
		for name, call := range calls {
			L.Push(L.NewFunction(func(L *LState) int { call(L); return 0 }))
			err := L.PCall(0, 0, nil)
			if err == nil || !strings.Contains(err.Error(), "attempt to modify a read-only table") {
				t.Errorf("%s: %s: error %v", d.name, name, err)
			}
		}
		L.Close()
	}

	// a Go function that panics: Lua's pcall gets the error
	L := NewState()
	defer L.Close()
	L.SetGlobal("t", tb)
	L.SetGlobal("gowrite", L.NewFunction(func(L *LState) int {
		L.CheckTable(1).RawSetString("x", LNumber(3))
		return 0
	}))
	if err := L.DoString(`
		local ok, err = pcall(gowrite, t)
		assert(not ok and err:find("attempt to modify a read-only table", 1, true), err)
	`); err != nil {
		t.Fatal(err)
	}
}

func TestFrozenTableDeep(t *testing.T) {
	L := NewState()
	defer L.Close()
	if err := L.DoString(`
		local leaf = {}
		key = {}
		tree = {a = {b = {c = leaf}}, list = {leaf, {}}, [key] = 1, f = function() end}
		tree.a.b.up = tree -- a cycle
		mt = {__index = {}}
		setmetatable(tree.list, mt)
		other = {}
	`); err != nil {
		t.Fatal(err)
	}
	tree := L.GetGlobal("tree").(*LTable)
	tree.FreezeDeep()
	if err := L.DoString(`
		for _, code in ipairs{"tree.x = 1", "tree.a.x = 1", "tree.a.b.x = 1", "tree.a.b.c.x = 1",
				"tree.list[3] = 1", "tree.list[2].x = 1", "key.x = 1", "mt.x = 1", "mt.__index.x = 1"} do
			local ok, err = pcall(loadstring(code))
			assert(not ok and err:find("attempt to modify a read-only table", 1, true), code)
		end
		other.x = 1
	`); err != nil {
		t.Fatal(err)
	}
}

// The assignment paths of the VM that test the flag.
func BenchmarkFrozenTableAssign(b *testing.B) {
	for _, c := range []struct{ name, code string }{
		{"array", `local t = {} for i = 1, 100 do t[i] = 0 end for n = 1, 1000 do for i = 1, 100 do t[i] = n end end`},
		{"field", `local t = {x = 0} for n = 1, 100000 do t.x = n end`},
		{"hash", `local t = {} local k = "k" for n = 1, 100000 do t[k] = n end`},
	} {
		b.Run(c.name, func(b *testing.B) {
			L := NewState()
			defer L.Close()
			fn, err := L.LoadString(c.code)
			if err != nil {
				b.Fatal(err)
			}
			for b.Loop() {
				L.Push(fn)
				if err := L.PCall(0, 0, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
