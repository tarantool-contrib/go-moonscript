package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
)

// TestGolden parses every testdata/parse/**/*.moon input and compares the
// result with the reference output recorded by scripts/gen-parse-golden.sh:
// <name>.tree holds the moonc -T tree, <name>.err the parse error message.
func TestGolden(t *testing.T) {
	var inputs []string
	err := filepath.WalkDir("../testdata/parse", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".moon") {
			inputs = append(inputs, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) == 0 {
		t.Fatal("no golden inputs found")
	}

	for _, path := range inputs {
		base := strings.TrimSuffix(path, ".moon")
		name, _ := filepath.Rel("../testdata/parse", base)
		t.Run(filepath.ToSlash(name), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			wantTree, treeErr := os.ReadFile(base + ".tree")
			wantErr, errErr := os.ReadFile(base + ".err")
			if (treeErr == nil) == (errErr == nil) {
				t.Fatalf("need exactly one of %s.tree / %s.err (run scripts/gen-parse-golden.sh)", base, base)
			}

			tree, perr := String(string(src))
			var got, want string
			if treeErr == nil {
				want = string(wantTree)
				if perr != nil {
					t.Fatalf("parse failed, want tree:\n%s\ngot error:\n%s", want, perr)
				}
				got = ast.Tree(tree) + "\n"
			} else {
				want = string(wantErr)
				if perr == nil {
					t.Fatalf("parse succeeded, want error:\n%s\ngot tree:\n%s", want, ast.Tree(tree))
				}
				got = perr.Error() + "\n"
			}
			if got != want {
				t.Errorf("mismatch\n--- want\n%s--- got\n%s", want, got)
			}
		})
	}
}

// TestConcurrentParse checks that the shared grammar holds no per-parse
// state (run with -race).
func TestConcurrentParse(t *testing.T) {
	inputs, _ := filepath.Glob("../testdata/parse/spec/*.moon")
	if len(inputs) == 0 {
		t.Skip("no inputs")
	}
	want := map[string]string{}
	for _, path := range inputs {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		tree, err := String(string(src))
		if err != nil {
			t.Fatal(err)
		}
		want[path] = ast.Tree(tree)
	}
	done := make(chan string)
	for w := 0; w < 4; w++ {
		go func() {
			for _, path := range inputs {
				src, _ := os.ReadFile(path)
				tree, err := String(string(src))
				if err != nil || ast.Tree(tree) != want[path] {
					done <- path
					return
				}
			}
			done <- ""
		}()
	}
	for w := 0; w < 4; w++ {
		if bad := <-done; bad != "" {
			t.Errorf("concurrent parse of %s differs", bad)
		}
	}
}
