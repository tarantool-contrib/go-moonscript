package lua

// go-moonscript patch: tests of the VM, coroutine and context fixes ported
// from other gopher-lua forks and pull requests (see moonscript.patch); the
// Lua-visible behaviour is tested against Lua 5.1 in testdata/compat
// (vm_*.lua).

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A coroutine that yields while a Go function it called is calling Lua
// (L.Call, L.PCall) gets Lua 5.1's error instead of resuming the wrong frame
// (yuin/gopher-lua#545), and the state stays usable.
func TestVMYieldAcrossGoCall(t *testing.T) {
	L := NewState()
	defer L.Close()
	L.SetGlobal("gocall", L.NewFunction(func(L *LState) int {
		L.Push(L.CheckFunction(1))
		L.Call(0, 1)
		return 1
	}))
	L.SetGlobal("gopcall", L.NewFunction(func(L *LState) int {
		L.Push(L.CheckFunction(1))
		err := L.PCall(0, 1, nil)
		if err != nil {
			L.Push(LString("caught: " + err.(*ApiError).Object.String()))
		}
		return 1
	}))
	err := L.DoString(`
	  local msg = "attempt to yield across metamethod/C-call boundary"
	  local co = coroutine.create(function()
	    return gocall(function() coroutine.yield(1) return 2 end)
	  end)
	  local ok, err = coroutine.resume(co)
	  assert(not ok and err == msg, tostring(err))
	  assert(coroutine.status(co) == "dead")
	  co = coroutine.create(function()
	    local r = gopcall(function() coroutine.yield(1) end)
	    coroutine.yield(r)
	    return "done"
	  end)
	  ok, err = coroutine.resume(co)
	  assert(ok and err == "caught: " .. msg, tostring(err))
	  ok, err = coroutine.resume(co)
	  assert(ok and err == "done", tostring(err))
	`)
	if err != nil {
		t.Fatal(err)
	}

	// through the Go API
	co, _ := L.NewThread()
	fn := L.NewFunction(func(L *LState) int {
		L.Push(L.NewFunction(func(L *LState) int { return L.Yield(LNumber(1)) }))
		L.Call(0, 0)
		return 0
	})
	st, err, _ := L.Resume(co, fn)
	if st != ResumeError || err == nil || !strings.Contains(err.Error(), "attempt to yield across") {
		t.Fatalf("got %v, %v", st, err)
	}
}

// A Go function run as a coroutine's body by L.Resume returns its results
// to the resumer and finishes the coroutine (it returned nothing and left
// the coroutine running), and one that yields returns the values it is
// resumed with when resumed (it crashed with a nil dereference); after
// either the state can still run coroutines. From mdogan/gopher-lua
// 76d62e4131 (TestCoroutineGoFunctionBody).
func TestVMGoFunctionCoroutineBody(t *testing.T) {
	L := NewState()
	defer L.Close()
	upper := L.GetField(L.GetGlobal("string"), "upper").(*LFunction)
	co, _ := L.NewThread()
	st, err, values := L.Resume(co, upper, LString("x"))
	if st != ResumeOK || err != nil || len(values) != 1 || values[0] != LString("X") {
		t.Fatalf("string.upper body: %v %v %v", st, err, values)
	}
	if s := L.Status(co); s != "dead" {
		t.Fatalf("status %q", s)
	}
	if L.G.CurrentThread != L {
		t.Fatal("the coroutine is still the current thread")
	}
	st, err, _ = L.Resume(co, upper)
	if st != ResumeError || err == nil || !strings.Contains(err.Error(), "dead") {
		t.Fatalf("resume dead: %v %v", st, err)
	}

	yield := L.NewFunction(func(L *LState) int { return L.Yield(LNumber(L.GetTop())) })
	co, _ = L.NewThread()
	st, err, values = L.Resume(co, yield, LNumber(1), LNumber(2))
	if st != ResumeYield || err != nil || len(values) != 1 || values[0] != LNumber(2) {
		t.Fatalf("yield body: %v %v %v", st, err, values)
	}
	if s := L.Status(co); s != "suspended" {
		t.Fatalf("status %q", s)
	}
	st, err, values = L.Resume(co, yield, LString("a"), LString("b"))
	if st != ResumeOK || err != nil || len(values) != 2 || values[0] != LString("a") || values[1] != LString("b") {
		t.Fatalf("resumed yield body: %v %v %v", st, err, values)
	}
	if s := L.Status(co); s != "dead" {
		t.Fatalf("status %q", s)
	}

	if err := L.DoString(`
	  local co = coroutine.create(function() coroutine.yield(1) return 2 end)
	  assert(select(2, coroutine.resume(co)) == 1)
	  assert(select(2, coroutine.resume(co)) == 2)
	  assert(coroutine.running() == nil)
	`); err != nil {
		t.Fatal(err)
	}
}

// The main thread is "normal" while a coroutine runs, and resuming it (or
// any active coroutine) fails instead of re-entering it (a fatal Go stack
// overflow); see testdata/compat/vm_coroutine_active.lua. From
// mdogan/gopher-lua afcb8e0069 (TestCoroutineResumeActiveAncestor).
func TestVMResumeActiveCoroutine(t *testing.T) {
	L := NewState()
	defer L.Close()
	L.SetGlobal("mainThread", L)
	L.SetGlobal("goresume", L.NewFunction(func(L *LState) int {
		st, err, _ := L.Resume(L.CheckThread(1), nil)
		if st != ResumeError || err == nil {
			L.RaiseError("resumed: %v", st)
		}
		L.Push(LString(err.Error()))
		return 1
	}))
	if err := L.DoString(`
	  local m = coroutine.create(function()
	    assert(coroutine.status(mainThread) == "normal", coroutine.status(mainThread))
	    local ok, err = coroutine.resume(mainThread)
	    assert(not ok and err == "cannot resume normal coroutine", tostring(err))
	    local a
	    a = coroutine.create(function() return goresume(a) end)
	    return goresume(mainThread), select(2, coroutine.resume(a))
	  end)
	  local ok, e1, e2 = coroutine.resume(m)
	  assert(ok, e1)
	  assert(string.find(e1, "cannot resume normal coroutine", 1, true), e1)
	  assert(string.find(e2, "running", 1, true), e2)
	  assert(coroutine.status(m) == "dead")
	`); err != nil {
		t.Fatal(err)
	}
}

type vmCtxKey struct{}

// A coroutine runs under the context of whoever resumes it: one created
// while the state had no context stops at the resumer's deadline, and one
// resumed by a later call sees that call's context values, not its
// creator's (and isn't stopped by the creator's cancelled context). From
// mdogan/gopher-lua f7b3485bd8 (TestContextLuaResumeUsesResumerDeadline,
// TestContextLuaResumeUsesResumerValues, TestContextLuaResumeNested).
func TestVMCoroutineResumerContext(t *testing.T) {
	for name, script := range map[string]string{
		"resume": `local ok, err = coroutine.resume(co); error(err)`,
		"wrap":   `w()`,
		"goapi":  `goresume(co)`,
	} {
		t.Run(name, func(t *testing.T) {
			L := NewState()
			defer L.Close()
			L.SetGlobal("goresume", L.NewFunction(func(L *LState) int {
				_, err, _ := L.Resume(L.CheckThread(1), nil)
				L.RaiseError("%v", err)
				return 0
			}))
			if err := L.DoString(`
			  co = coroutine.create(function() while true do end end)
			  w = coroutine.wrap(function() while true do end end)
			`); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			L.SetContext(ctx)
			done := make(chan error, 1)
			go func() { done <- L.DoString(script) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
					t.Fatalf("unexpected error: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("coroutine still running 5s after a 100ms deadline")
			}
		})
	}

	L := NewState()
	defer L.Close()
	L.SetGlobal("who", L.NewFunction(func(L *LState) int {
		if L.Context() == nil {
			L.Push(LString("none"))
		} else {
			L.Push(LString(L.Context().Value(vmCtxKey{}).(string)))
		}
		return 1
	}))
	first, cancelFirst := context.WithCancel(context.WithValue(context.Background(), vmCtxKey{}, "first"))
	L.SetContext(first)
	if err := L.DoString(`
	  lua_co = coroutine.create(function()
	    while true do coroutine.yield(who()) end
	  end)
	  inner = coroutine.wrap(function() while true do coroutine.yield(who()) end end)
	  outer = coroutine.create(function() while true do coroutine.yield(inner()) end end)
	  local _, v = coroutine.resume(lua_co)
	  assert(v == "first", v)
	`); err != nil {
		t.Fatal(err)
	}
	if th := L.GetGlobal("lua_co").(*LState); th.ctx != nil || th.ctxCancelFn != nil {
		t.Error("coroutine.create gave the thread a context")
	}
	cancelFirst()
	second, cancelSecond := context.WithCancel(context.WithValue(context.Background(), vmCtxKey{}, "second"))
	defer cancelSecond()
	L.SetContext(second)
	if err := L.DoString(`
	  local ok, v = coroutine.resume(lua_co)
	  assert(ok, v)
	  assert(v == "second", v)
	  ok, v = coroutine.resume(outer)
	  assert(ok and v == "second", tostring(v))
	`); err != nil {
		t.Fatal(err)
	}
	// suspended threads don't keep the resumer's context
	L.RemoveContext()
	if err := L.DoString(`
	  local ok, v = coroutine.resume(outer)
	  assert(ok and v == "none", tostring(v))
	`); err != nil {
		t.Fatal(err)
	}
	st, err, values := L.Resume(L.GetGlobal("lua_co").(*LState), nil)
	if st != ResumeYield || err != nil || values[0] != LString("none") {
		t.Fatalf("%v %v %v", st, err, values)
	}
}

// With Options.IncludeGoStackTrace, a Go panic in a coroutine gives the Go
// stack as PCall does (yuin/gopher-lua#529, #539 by eliotee).
func TestVMCoroutineGoStackTrace(t *testing.T) {
	for _, include := range []bool{true, false} {
		L := NewState(Options{IncludeGoStackTrace: include})
		L.Register("fail", func(L *LState) int { panic("coroutine panic") })
		fn, err := L.LoadString(`fail()`)
		if err != nil {
			t.Fatal(err)
		}
		co, _ := L.NewThread()
		st, err, _ := L.Resume(co, fn)
		if st != ResumeError || err == nil {
			t.Fatalf("got %v %v", st, err)
		}
		L.SetGlobal("fn", fn)
		if err := L.DoString(`ok, result = coroutine.resume(coroutine.create(fn))`); err != nil {
			t.Fatal(err)
		}
		for _, msg := range []string{err.(*ApiError).Object.String(), L.GetGlobal("result").String()} {
			hasStack := strings.Contains(msg, "goroutine ") && strings.Contains(msg, "TestVMCoroutineGoStackTrace")
			if !strings.HasPrefix(msg, "coroutine panic") || hasStack != include {
				t.Errorf("IncludeGoStackTrace %v: %q", include, msg)
			}
		}
		L.Close()
	}
}

// The registry overflowing while a tail call sets up its frame (a function
// tail-calling itself with more and more arguments) is a Lua error;
// looking up the position of that frame, which has run no instruction yet,
// indexed its line table at -1: a Go panic, also in the error handler of
// PCall, which killed the process (yuin/gopher-lua#282).
func TestVMRegistryOverflowTailCall(t *testing.T) {
	L := NewState()
	defer L.Close()
	const f = `local function f(...) return f(1, 2, 3, ...) end `
	err := L.DoString(f + `f()`)
	if err == nil || !strings.Contains(err.Error(), "<string>: registry overflow") {
		t.Fatalf("unprotected: %v", err)
	}
	if err := L.DoString(f + `
		local ok, msg = pcall(f)
		assert(not ok and msg == "<string>: registry overflow", msg)
		ok, msg = xpcall(f, debug.traceback)
		assert(not ok and msg:find("registry overflow", 1, true), msg)
		ok, msg = coroutine.resume(coroutine.create(f))
		assert(not ok and msg == "<string>: registry overflow", msg)
	`); err != nil {
		t.Fatal(err)
	}
}
