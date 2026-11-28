package lua

// go-moonscript patch: tests of the debug hooks (hook.go). Their event
// sequences are compared with PUC Lua 5.1's in go-moonscript/runtime
// (TestHooks); these check the Go API and that hooks don't disturb the code
// they observe.

import (
	"fmt"
	"os"
	"testing"
)

func runChunk(t *testing.T, L *LState, src string) string {
	t.Helper()
	fn, err := L.LoadString(src)
	if err != nil {
		t.Fatal(err)
	}
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		t.Fatal(err)
	}
	defer L.Pop(1)
	return L.Get(-1).String()
}

// TestHookPreservesState runs testdata/hookstate.lua with each kind of hook
// set (the hook runs Lua code and calls functions for every event) and
// checks that its result is the same as without hooks.
func TestHookPreservesState(t *testing.T) {
	src, err := os.ReadFile("testdata/hookstate.lua")
	if err != nil {
		t.Fatal(err)
	}
	want := runChunk(t, NewState(), string(src))
	for _, mask := range []string{"c", "r", "l", "crl"} {
		for _, count := range []int{0, 1, 3} {
			L := NewState()
			L.SetGlobal("src", LString(src))
			got := runChunk(t, L, fmt.Sprintf(`
local events = 0
local function hook(event, line)
  local info = debug.getinfo(2, "Sl")
  events = events + 1
  local s = tostring(line) .. tostring(info.source) .. select("#", event, line)
end
local f = assert(loadstring(src))
debug.sethook(hook, %q, %d)
local result = f()
debug.sethook()
assert(events > 0, "no events")
return result`, mask, count))
			if got != want {
				t.Errorf("mask %q count %d: got %q, want %q", mask, count, got, want)
			}
			L.Close()
		}
	}
}

// TestSetHookAPI checks LState.SetHook and GetHook against debug.gethook.
func TestSetHookAPI(t *testing.T) {
	L := NewState()
	defer L.Close()
	if fn, mask, count := L.GetHook(); fn != LNil || mask != "" || count != 0 {
		t.Errorf("no hook: GetHook() = %v, %q, %d", fn, mask, count)
	}
	var lines []int
	hook := L.NewFunction(func(L *LState) int {
		if L.CheckString(1) == "line" {
			lines = append(lines, L.CheckInt(2))
		}
		return 0
	})
	L.SetHook(hook, "lc", 0)
	if fn, mask, count := L.GetHook(); fn != hook || mask != "cl" || count != 0 {
		t.Errorf("GetHook() = %v, %q, %d", fn, mask, count)
	}
	if got := runChunk(t, L, "local a = 1\nlocal b = a + 1\nlocal _, m = debug.gethook()\nreturn m"); got != "cl" {
		t.Errorf("debug.gethook mask = %q", got)
	}
	L.SetHook(nil, "", 0)
	if fmt.Sprint(lines[:3]) != "[1 2 3]" {
		t.Errorf("line events %v", lines)
	}
	if fn, _, _ := L.GetHook(); fn != LNil {
		t.Errorf("hook still set: %v", fn)
	}
}

// TestHookNoHookInHook checks that a hook runs with hooks off and that they
// are back on after it returns or raises an error.
func TestHookNoHookInHook(t *testing.T) {
	L := NewState()
	defer L.Close()
	got := runChunk(t, L, `
local depth, maxdepth, calls, armed = 0, 0, 0, false
local function helper() return 1 end
local function hook(event)
  calls = calls + 1
  depth = depth + 1
  if depth > maxdepth then maxdepth = depth end
  helper()
  depth = depth - 1
  if armed and event == "line" then
    armed = false
    error("from hook")
  end
end
debug.sethook(hook, "crl")
local ok = pcall(function()
  armed = true
  local x = helper()
  return x
end)
local after = calls
local y = helper()
debug.sethook()
return maxdepth .. " " .. tostring(ok) .. " " .. tostring(calls > after)`)
	if got != "1 false true" {
		t.Errorf("got %q, want %q", got, "1 false true")
	}
}
