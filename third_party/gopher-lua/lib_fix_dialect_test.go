package lua

// go-moonscript patch: tests of library behaviour that depends on the
// dialect. Every testdata/dialect/<name>.luajit.lua runs on a DialectLuaJIT
// state and its output is compared with ref/<name>.luajit.out, the output
// of LuaJIT 2.1 (luajit in the luajitref:2.1 image, run in that
// directory); <name>.lua5.1.lua likewise on DialectLua51 against lua5.1.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDialectScripts(t *testing.T) {
	scripts, err := filepath.Glob("testdata/dialect/*.lua")
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) == 0 {
		t.Fatal("no scripts")
	}
	for _, script := range scripts {
		name := strings.TrimSuffix(filepath.Base(script), ".lua")
		dialect := DialectLua51
		if strings.HasSuffix(name, ".luajit") {
			dialect = DialectLuaJIT
		}
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("testdata/dialect/ref", name+".out"))
			if err != nil {
				t.Fatal(err)
			}
			if got := runDialectScript(t, script, dialect); got != string(want) {
				t.Errorf("output differs from the reference's:\n--- got\n%s\n--- want\n%s", got, want)
			}
		})
	}
}

// runDialectScript is runCompatScript on a state of the given dialect.
func runDialectScript(t *testing.T, script string, dialect Dialect) string {
	t.Helper()
	code, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
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
	fn, err := L.Load(strings.NewReader(string(code)), filepath.Base(script))
	if err != nil {
		t.Fatal(err)
	}
	L.Push(fn)
	if err := L.PCall(0, 0, nil); err != nil {
		t.Fatalf("%v\noutput so far:\n%s", err, out.String())
	}
	return out.String()
}
