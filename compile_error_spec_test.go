package moonscript

// Port of upstream v0.8.0 spec/compile_error_spec.moon: the user facing
// compile errors raised by compile.tree (raw message, without the
// "failed to compile" formatting) and the line they point at.

import (
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/compiler"
	"github.com/tarantool-contrib/go-moonscript/parse"
)

// unindent is spec/helpers.moon's unindent applied to a Lua long string:
// drop the newline right after the opening quote (as [[ does), strip the
// indentation of the first line from every line, then trim.
func unindent(s string) string {
	s = strings.TrimPrefix(s, "\n")
	indent := s[:len(s)-len(strings.TrimLeft(s, " \t\n\r\v\f"))]
	if indent == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\n"+indent, "\n")
	return strings.Trim(s, " \t\n\r\v\f")
}

var compileErrorSpecCases = []struct {
	name, code, msg string
	line            int
}{
	{"short-colon stub outside of with", `
		print "hello"
		x = \foo
	`, "Short-colon syntax must be called within a with block", 2},
	{"short-dot outside of with", `
		print "hello"
		print "world"
		x = .field
	`, "Short-dot syntax must be called within a with block", 3},
	{"destructuring invalid value", `
		print "hello"
		{1} = thing
	`, "Can't destructure value of type: number", 2},
	{"destructuring into a function call", `
		print "hello"
		{foo!} = thing
	`, "Can't destructure into chain ending in call", 2},
	{"destructuring function argument into call", `
		print "hello"
		f = ({foo!}) -> foo
	`, "Can't destructure into chain ending in call", 2},
	{"destructuring function argument into invalid value", `
		print "hello"
		print "world"
		f = ({1}) -> nil
	`, "Can't destructure value of type: number", 3},
	{"destructured parameter followed by parameter of same name", `
		print "hello"
		f = ({:a}, a) -> a
	`, "Can't destructure into 'a': name is bound by another parameter", 2},
	{"destructured parameter preceded by parameter of same name", `
		print "hello"
		f = (a, {:a}) -> a
	`, "Can't destructure into 'a': name is bound by another parameter", 2},
	{"destructured parameter followed by self parameter of same name", `
		print "hello"
		f = ({:a}, @a) => a
	`, "Can't destructure into 'a': name is bound by another parameter", 2},
	{"destructured self parameter in fat arrow", `
		print "hello"
		f = ({:self}) => @x
	`, "Can't destructure into 'self': name is bound by another parameter", 2},
	{"two destructured parameters binding same name", `
		print "hello"
		f = ({:a}, {a: a}) -> a
	`, "Can't destructure into 'a': name is bound by another parameter", 2},
	{"continue outside of loop", `
		print "hello"
		continue
	`, "continue must be inside of a loop", 2},
}

func TestCompileErrorSpec(t *testing.T) {
	for _, c := range compileErrorSpecCases {
		t.Run(c.name, func(t *testing.T) {
			src := unindent(c.code)
			tree, err := parse.String(src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			code, _, cerr := compiler.Tree(tree, compiler.Options{})
			if cerr == nil {
				t.Fatalf("expected compile to fail, got\n%s", code)
			}
			if cerr.Msg != c.msg {
				t.Errorf("message = %q, want %q", cerr.Msg, c.msg)
			}
			line := 0
			if cerr.Pos != 0 {
				line = PosToLine(src, cerr.Pos)
			}
			if line != c.line {
				t.Errorf("line = %d, want %d", line, c.line)
			}
		})
	}
}

// a malformed interpolation must fail the parse instead of falling through
// to literal string content
func TestCompileErrorSpecInvalidInterpolation(t *testing.T) {
	for _, src := range []string{
		`x = "one #{three ..} two"`,
		`x = "abc#{"`,
		`x = "#{}"`,
	} {
		tree, err := parse.String(src)
		if tree != nil || err == nil {
			t.Errorf("expected parse to fail: %s", src)
		}
	}
}
