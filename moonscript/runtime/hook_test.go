package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	lua "github.com/tarantool-contrib/go-moonscript/lua"
)

// TestHooks runs the scripts in testdata/hooks, which record debug hook
// events (debug.sethook: call, return, tail return, line and count events,
// debug.getinfo inside hooks, per-coroutine hooks, debug.gethook) and print
// them, and compares their output with PUC Lua 5.1's, recorded by
// scripts/gen-hook-ref.sh in testdata/hooks/ref.
func TestHooks(t *testing.T) {
	scripts, err := filepath.Glob("testdata/hooks/*.lua")
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) == 0 {
		t.Fatal("no scripts")
	}
	for _, script := range scripts {
		name := strings.TrimSuffix(filepath.Base(script), ".lua")
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("testdata/hooks/ref", name+".out"))
			if err != nil {
				t.Fatal(err)
			}
			if got := runHookScript(t, script); got != string(want) {
				t.Errorf("output differs from lua5.1's:\n--- got\n%s\n--- want\n%s", got, want)
			}
		})
	}
}

// runHookScript runs a script as `lua5.1 <name>.lua` would in its
// directory (chunk name "@<name>.lua") and returns what it printed. The
// scripts print with their hooks off, so print is replaced by one that
// writes to a buffer, formatting values with the global tostring as Lua
// 5.1's print does.
func runHookScript(t *testing.T, script string) string {
	t.Helper()
	code, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	L := NewState()
	defer L.Close()
	var out strings.Builder
	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		tostring := L.GetGlobal("tostring")
		for i := 1; i <= L.GetTop(); i++ {
			if i > 1 {
				out.WriteByte('\t')
			}
			L.Push(tostring)
			L.Push(L.Get(i))
			L.Call(1, 1)
			out.WriteString(L.ToString(-1))
			L.Pop(1)
		}
		out.WriteByte('\n')
		return 0
	}))
	fn, err := LoadLua(L, string(code), "@"+filepath.Base(script))
	if err != nil {
		t.Fatal(err)
	}
	L.Push(fn)
	if err := L.PCall(0, 0, nil); err != nil {
		t.Fatalf("%v\noutput so far:\n%s", err, out.String())
	}
	return out.String()
}
