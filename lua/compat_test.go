package lua

// go-moonscript patch: Lua 5.1 compatibility tests of the fixes ported from
// other gopher-lua forks and pull requests (see moonscript.patch). Every
// script in testdata/compat is run here and its output compared with the
// reference interpreter's, recorded by scripts/gen-compat-ref.sh in
// testdata/compat/ref: PUC Lua 5.1.5 for <name>.lua, run with
// DialectLua51, and LuaJIT 2.1 for <name>.jit.lua, run with DialectLuaJIT
// (the LuaJIT dialect: its extensions over Lua 5.1).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompat(t *testing.T) {
	scripts, err := filepath.Glob("testdata/compat/*.lua")
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) == 0 {
		t.Fatal("no scripts")
	}
	for _, script := range scripts {
		name := strings.TrimSuffix(filepath.Base(script), ".lua")
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("testdata/compat/ref", name+".out"))
			if err != nil {
				t.Fatal(err)
			}
			if got := runCompatScript(t, script); got != string(want) {
				t.Errorf("output differs from the reference's:\n--- got\n%s\n--- want\n%s", got, want)
			}
		})
	}
}

// runCompatScript runs a script as `lua5.1 <name>.lua` (or `luajit
// <name>.jit.lua`, with DialectLuaJIT) would in its directory (chunk name
// "@<name>.lua", the script's directory the working directory) and returns
// what it printed. print writes to a buffer, formatting values with the
// global tostring as Lua 5.1's print does.
func runCompatScript(t *testing.T, script string) string {
	t.Helper()
	return runCompatScriptWith(t, script, func(L *LState, code []byte, chunkname string) (*LFunction, error) {
		return L.LoadChunk(strings.NewReader(string(code)), chunkname)
	})
}

// runCompatScriptWith is runCompatScript, the script loaded by load.
func runCompatScriptWith(t *testing.T, script string, load func(L *LState, code []byte, chunkname string) (*LFunction, error)) string {
	t.Helper()
	dialect := DialectLua51
	if strings.HasSuffix(script, ".jit.lua") {
		dialect = DialectLuaJIT
	}
	code, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(script))
	L := NewState(Options{Dialect: dialect})
	defer L.Close()
	var out strings.Builder
	L.SetGlobal("print", L.NewFunction(func(L *LState) int {
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
	fn, err := load(L, code, "@"+filepath.Base(script))
	if err != nil {
		t.Fatal(err)
	}
	L.Push(fn)
	if err := L.PCall(0, 0, nil); err != nil {
		t.Fatalf("%v\noutput so far:\n%s", err, out.String())
	}
	return out.String()
}
