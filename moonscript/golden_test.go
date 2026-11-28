package moonscript

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/moonscript/compiler"
	"github.com/tarantool-contrib/go-moonscript/moonscript/parse"
)

// TestSpecOutputs mirrors upstream spec/lang_spec.moon: every
// testdata/spec/inputs/*.moon must compile to exactly
// testdata/spec/outputs/*.lua.
func TestSpecOutputs(t *testing.T) {
	inputs, _ := filepath.Glob("testdata/spec/inputs/*.moon")
	if len(inputs) == 0 {
		t.Fatal("no inputs")
	}
	for _, in := range inputs {
		name := strings.TrimSuffix(filepath.Base(in), ".moon")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata/spec/outputs", name+".lua"))
			if err != nil {
				t.Fatal(err)
			}
			tree, err := parse.String(string(src))
			if err != nil {
				t.Fatal(err)
			}
			code, _, cerr := compiler.Tree(tree, compiler.Options{})
			if cerr != nil {
				t.Fatal(compiler.FormatError(cerr.Msg, cerr.Pos, string(src)))
			}
			if code != string(want) {
				t.Errorf("output mismatch\n%s", firstDiff(string(want), code))
			}
		})
	}
}

func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g || i >= len(wl) || i >= len(gl) {
			lo := max(i-3, 0)
			ctx := strings.Join(gl[lo:min(i+3, len(gl))], "\n")
			return "line " + itoa(i+1) + ":\n  want: " + w + "\n  got:  " + g + "\n--- got context ---\n" + ctx
		}
	}
	return "(no line difference)"
}

func itoa(i int) string { return strconv.Itoa(i) }
