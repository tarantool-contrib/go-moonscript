package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/parse"
)

// TestClassSnippets compiles compiler/testdata/class/*.moon and compares with
// the reference `moonc -p` output stored next to each input (print adds the
// trailing newline).
func TestClassSnippets(t *testing.T) {
	inputs, _ := filepath.Glob("testdata/class/*.moon")
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
			want, err := os.ReadFile(strings.TrimSuffix(in, ".moon") + ".lua")
			if err != nil {
				t.Fatal(err)
			}
			tree, perr := parse.String(string(src))
			if perr != nil {
				t.Fatal(perr)
			}
			code, _, cerr := Tree(tree, Options{})
			if cerr != nil {
				t.Fatal(FormatError(cerr.Msg, cerr.Pos, string(src)))
			}
			if code+"\n" != string(want) {
				t.Errorf("output mismatch\n--- want ---\n%s--- got ---\n%s", want, code)
			}
		})
	}
}

// TestClassNameError checks the error raised for a class name that can't be
// turned into a __name value.
func TestClassNameError(t *testing.T) {
	tree, perr := parse.String("x = 1\nclass @Foo\n")
	if perr != nil {
		t.Fatal(perr)
	}
	_, _, cerr := Tree(tree, Options{})
	if cerr == nil || !strings.Contains(cerr.Msg, "don't know how to extract name from table") {
		t.Fatalf("expected name extraction error, got %v", cerr)
	}
}

// TestClassInterpolatedKeySuper: super inside a method with an interpolated
// string key fails like the reference (which crashes compiling the reused
// key node) instead of producing broken Lua.
func TestClassInterpolatedKeySuper(t *testing.T) {
	tree, perr := parse.String("class Foo extends Bar\n  \"#{name}\": => super!\n")
	if perr != nil {
		t.Fatal(perr)
	}
	_, _, cerr := Tree(tree, Options{})
	if cerr == nil || !strings.Contains(cerr.Msg, "attempt to call method 'gsub' (a nil value)") {
		t.Fatalf("expected gsub error, got %v", cerr)
	}
}
