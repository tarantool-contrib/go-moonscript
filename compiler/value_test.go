package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/parse"
)

// TestValueSnippets compiles compiler/testdata/value/*.moon (value
// transformers and destructuring) and compares with the reference `moonc -p`
// output stored next to each input: <name>.lua for code (print adds the
// trailing newline) or <name>.err for an expected error (moonc's
// "<file>\t<message>" stderr line).
func TestValueSnippets(t *testing.T) {
	inputs, _ := filepath.Glob("testdata/value/*.moon")
	if len(inputs) == 0 {
		t.Fatal("no inputs")
	}
	for _, in := range inputs {
		base := strings.TrimSuffix(in, ".moon")
		t.Run(filepath.Base(base), func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			code, errMsg := valTestCompile(t, string(src))
			if want, err := os.ReadFile(base + ".err"); err == nil {
				w := string(want)
				if i := strings.IndexByte(w, '\t'); i >= 0 {
					w = w[i+1:]
				}
				if strings.TrimRight(errMsg, "\n") != strings.TrimRight(w, "\n") {
					t.Errorf("error mismatch\n--- want ---\n%s\n--- got ---\n%s\n(code: %s)", w, errMsg, code)
				}
				return
			}
			want, err := os.ReadFile(base + ".lua")
			if err != nil {
				t.Fatal(err)
			}
			if errMsg != "" {
				t.Fatal(errMsg)
			}
			if code+"\n" != string(want) {
				t.Errorf("output mismatch\n--- want ---\n%s--- got ---\n%s", want, code)
			}
		})
	}
}

func valTestCompile(t *testing.T, src string) (code, errMsg string) {
	t.Helper()
	tree, perr := parse.String(src)
	if perr != nil {
		return "", perr.Error()
	}
	code, _, cerr := Tree(tree, Options{})
	if cerr != nil {
		return "", FormatError(cerr.Msg, cerr.Pos, src)
	}
	return code, ""
}
