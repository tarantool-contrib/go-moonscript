package lua

// go-moonscript patch: tests of gc.go's Go API side (the Lua side is in
// testdata/compat/gc_*.lua): the memory limit, the finalizers at Close,
// threads made by NewThread as roots, Go references and weak tables.

import (
	"strings"
	"testing"
)

func TestMemoryLimit(t *testing.T) {
	L := NewState(Options{MemoryLimit: 64 << 20})
	defer L.Close()
	if got := L.MemoryLimit(); got != 64<<20 {
		t.Fatalf("MemoryLimit() = %d", got)
	}
	for _, code := range []string{
		`return string.rep("x", 1e9)`,
		`return string.rep("abc", 2^62)`,
		`local s = string.rep("x", 2^20) local t = {} for i = 1, 1000 do t[i] = s .. i end`,
		`local s = string.rep("x", 2^24) return s .. s .. s .. s .. s .. s .. s .. s`,
		`local t = {} for i = 1, 2^27 do t[i] = true end`,
		`local t = {} for i = 1, 2^25 do t["k" .. i] = true end`,
		`local s = string.rep("x", 2^20) return table.concat({s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s, s})`,
		`return (string.gsub(string.rep("x", 2^16), "x", string.rep("y", 2^12)))`,
		`return string.format("%s%s%s%s%s%s%s%s", string.rep("x", 2^23), string.rep("x", 2^23), string.rep("x", 2^23), string.rep("x", 2^23), string.rep("x", 2^23), string.rep("x", 2^23), string.rep("x", 2^23), string.rep("x", 2^23))`,
	} {
		if err := L.DoString(`local ok, err = pcall(function() ` + code + ` end)
			collectgarbage()
			assert(not ok, "no error")
			assert(err == "not enough memory", err)`); err != nil {
			t.Errorf("%s: %v", code, err)
		}
	}
	// the state still works
	if err := L.DoString(`local t = {} for i = 1, 1000 do t[i] = string.rep("x", 1000) .. i end assert(#t == 1000)`); err != nil {
		t.Error(err)
	}
}

func TestMemoryLimitDefault(t *testing.T) {
	L := NewState()
	defer L.Close()
	if L.MemoryLimit() != 0 {
		t.Fatal("a limit by default")
	}
	if err := L.DoString(`local s = string.rep("x", 2^26) assert(#(s .. s) == 2^27)`); err != nil {
		t.Error(err)
	}
}

func TestCloseRunsFinalizers(t *testing.T) {
	L := NewState()
	var out []string
	L.SetGlobal("log", L.NewFunction(func(L *LState) int {
		out = append(out, L.CheckString(1))
		return 0
	}))
	if err := L.DoString(`
		local first
		for i = 1, 3 do
			local u = newproxy(first or true)
			first = first or u
			local mt = getmetatable(u)
			mt.__gc = function(o) log(mt[o]) end
			mt[u] = "u" .. i
		end
		local e = newproxy(true)
		getmetatable(e).__gc = function() error("ignored") end
		keep = e
	`); err != nil {
		t.Fatal(err)
	}
	L.Close()
	// newest first (lua_close), errors ignored; the proxies are keys of
	// their metatable, so they are reachable until the state is closed
	if got := strings.Join(out, " "); got != "u3 u2 u1" {
		t.Errorf("finalizers at Close: %q", got)
	}
}

func TestGoUserDataGC(t *testing.T) {
	L := NewState()
	defer L.Close()
	mt := L.NewTable()
	n := 0
	mt.RawSetString("__gc", L.NewFunction(func(L *LState) int {
		if ud := L.CheckUserData(1); ud.Value == "go" {
			n++
		}
		return 0
	}))
	ud := L.NewUserData()
	ud.Value = "go"
	L.SetMetatable(ud, mt)
	L.SetGlobal("ud", ud)
	if err := L.DoString(`collectgarbage()`); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("finalized while reachable")
	}
	if err := L.DoString(`ud = nil collectgarbage()`); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("__gc ran %d times", n)
	}
}

func TestNewThreadIsRoot(t *testing.T) {
	L := NewState()
	defer L.Close()
	th, _ := L.NewThread()
	if err := L.DoString(`fin = 0`); err != nil {
		t.Fatal(err)
	}
	fn, err := L.LoadString(`
		local p = newproxy(true)
		getmetatable(p).__gc = function() fin = fin + 1 end
		coroutine.yield()
		return p`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err, _ := L.Resume(th, fn); err != nil {
		t.Fatal(err)
	}
	if err := L.DoString(`collectgarbage() assert(fin == 0, "finalized on a live thread")`); err != nil {
		t.Fatal(err)
	}
	st, err, _ := L.Resume(th, fn)
	if err != nil || st != ResumeOK {
		t.Fatal(st, err)
	}
}

func TestWeakTableGoReference(t *testing.T) {
	L := NewState()
	defer L.Close()
	held := L.NewTable() // only Go holds it
	w := L.NewTable()
	L.SetMetatable(w, L.NewTable())
	L.GetMetatable(w).(*LTable).RawSetString("__mode", LString("v"))
	L.SetMetatable(w, L.GetMetatable(w)) // registered with its __mode
	w.RawSetInt(1, held)
	w.RawSetInt(2, L.NewTable())
	L.SetGlobal("w", w)
	if err := L.DoString(`collectgarbage() assert(w[1] and w[2] == nil)`); err != nil {
		t.Fatal(err)
	}
	if w.RawGetInt(1) != held {
		t.Error("the entry kept by Go changed")
	}
}

// Close after an error left the stack full (the pattern of a deferred
// Close behind a recover) still runs the finalizers, on an emptied stack as
// lua_close does: the io library's files have __gc.
func TestCloseOnFullStack(t *testing.T) {
	L := NewState(Options{RegistryMaxSize: 300, RegistrySize: 200, RegistryGrowStep: 25})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected a registry overflow")
			}
		}()
		for i := 0; i < 301; i++ {
			L.Push(LString("x"))
		}
	}()
	L.Close()
}

// A cycle that frees Lua garbage lowers the count (gc.lua's dosteps), every
// time: the count is read exactly (the allocations from the spans cached by
// the Ps are counted), and the collector's scratch, which grows when a cycle
// marks more objects or sweeps more weak references than before, is not
// counted. The tables of a are new marked objects, those of w new weak
// references, at each round.
func TestCountAfterCycle(t *testing.T) {
	L := NewState()
	defer L.Close()
	if err := L.DoString(`
		local keep = {}
		for round = 1, 60 do
			collectgarbage()
			collectgarbage("stop")
			local a, w = {}, setmetatable({}, {__mode = "k"})
			for i = 1, 100 + round * 20 do a[i] = {{}}; w[{}] = i; local b = {} end
			keep[round] = a
			local x = gcinfo()
			repeat until collectgarbage("step", round % 3)
			local y = gcinfo()
			assert(y < x, "round " .. round .. ": " .. x .. " Kbytes, then " .. y)
			assert(next(w) == nil)
			collectgarbage("restart")
		end`); err != nil {
		t.Fatal(err)
	}
}
