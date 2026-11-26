package compiler

import (
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/parse"
)

func stmCompile(t *testing.T, src string) (string, *CompileError) {
	t.Helper()
	tree, err := parse.String(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	code, _, cerr := Tree(tree, Options{})
	return code, cerr
}

func TestStatementSnippets(t *testing.T) {
	for _, c := range stmSnippetCases {
		t.Run(c.name, func(t *testing.T) {
			got, cerr := stmCompile(t, c.src+"\n")
			if cerr != nil {
				t.Fatalf("compile error: %s", cerr.Msg)
			}
			if got != c.want {
				t.Errorf("mismatch\nsource:\n%s\nwant:\n%s\ngot:\n%s", c.src, c.want, got)
			}
		})
	}
}

func TestStatementContinueOutsideLoop(t *testing.T) {
	src := "continue\n"
	_, cerr := stmCompile(t, src)
	if cerr == nil {
		t.Fatal("expected an error")
	}
	got := FormatError(cerr.Msg, cerr.Pos, src)
	if !strings.HasPrefix(got, "failed to compile: continue must be inside of a loop at line 1, column 1:") {
		t.Errorf("unexpected error: %q", got)
	}
}
