package parse

// Port of upstream v0.8.0 spec/parser_spec.moon: hand-written expected
// trees, including the [-1] position slots (Node.Pos), compared like
// busted's assert.same.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/ast"
)

// sameTree compares two parse values exactly, positions included; it
// returns a description of the first difference.
func sameTree(want, got any) string {
	w, ok := want.(*ast.Node)
	if !ok {
		if want != got {
			return fmt.Sprintf("want %s, got %s", ast.FlatValue(want), ast.FlatValue(got))
		}
		return ""
	}
	g, ok := got.(*ast.Node)
	if !ok || g == nil {
		return fmt.Sprintf("want %s, got %s", ast.FlatValue(w), ast.FlatValue(got))
	}
	if w.Pos != g.Pos {
		return fmt.Sprintf("[-1]: want %d, got %d in %s", w.Pos, g.Pos, ast.FlatValue(g))
	}
	if w.Len() != g.Len() {
		return fmt.Sprintf("want %s, got %s", ast.FlatValue(w), ast.FlatValue(g))
	}
	for i := range w.Items {
		if d := sameTree(w.Items[i], g.Items[i]); d != "" {
			return fmt.Sprintf("[%d] %s", i+1, d)
		}
	}
	return ""
}

func mustParse(t *testing.T, src string) *ast.Node {
	t.Helper()
	tree, err := String(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return tree
}

func TestParserSpecTrees(t *testing.T) {
	n, p := ast.New, ast.NewPos
	cases := []struct {
		name, src string
		want      *ast.Node
	}{
		{"parses assignment", "x = 5", n(
			p(1, "assign",
				n(p(1, "ref", "x")),
				n(p(4, "number", "5"))),
		)},
		{"parses an open call", "print x", n(
			p(1, "chain",
				p(1, "ref", "print"),
				n("call", n(p(6, "ref", "x")))),
		)},
		{"parses table with self-assign and string key", `t = {:name, "k": v}`, n(
			p(1, "assign",
				n(p(1, "ref", "t")),
				n(p(4, "table", n(
					n(n("key_literal", "name"), p(11, "ref", "name")),
					n(n("string", `"`, "k"), p(17, "ref", "v")),
				)))),
		)},
		{"parses a function literal with binary op body", "f = (a) -> a + 1", n(
			p(1, "assign",
				n(p(1, "ref", "f")),
				n(p(4, "fndef", n(n("a")), n(), "slim", n(
					p(11, "exp", p(11, "ref", "a"), "+", p(15, "number", "1")),
				)))),
		)},
		{"parses an indented block", "if x\n  y!", n(
			p(1, "if", p(3, "ref", "x"), n(
				p(8, "chain", p(8, "ref", "y"), n("call", n())),
			)),
		)},
		{"parses lua strings, reconstructing the open delimiter", "x = [==[str]==]", n(
			p(1, "assign",
				n(p(1, "ref", "x")),
				n(p(4, "string", "[==[", "str"))),
		)},
		{"parses an empty file", "", n()},
		{"parses a comment-only file", "-- nothing here", n()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mustParse(t, c.src)
			if d := sameTree(c.want, got); d != "" {
				t.Errorf("%s\nwant %s\ngot  %s", d, ast.Tree(c.want), ast.Tree(got))
			}
		})
	}
}

func TestParserSpecAnonymousClass(t *testing.T) {
	tree := mustParse(t, "x = class")
	cls := tree.At(1).(*ast.Node).At(3).(*ast.Node).At(1).(*ast.Node)
	if cls.At(1) != "class" {
		t.Errorf("[1] = %v, want class", cls.At(1))
	}
	// false, not nil: a hole would make the node's length unpredictable
	if cls.At(2) != false {
		t.Errorf("[2] = %#v, want false", cls.At(2))
	}
	if cls.At(3) != "" {
		t.Errorf("[3] = %#v, want \"\"", cls.At(3))
	}
	if d := sameTree(ast.New(), cls.At(4)); d != "" {
		t.Errorf("[4]: %s", d)
	}
}

func TestParserSpecFailures(t *testing.T) {
	cases := []struct {
		name, src string
		matches   []string // Lua patterns used by the spec, as regexps
	}{
		{"fails on unbalanced parens", "x = (a + b", nil},
		{"fails on a bad outdent", "if x\n    y\n  z", nil},
		{"rejects a non-assignable left hand side", "f! = 5", []string{"not assignable", "line 1"}},
		{"reports a line position for parse failures", "x = 1\ny = 2\nz = (a +", []string{"line 3"}},
		{"reports a line position for parse failures", "x = \"hello\ny = 1", []string{"line 2"}},
		{"reports a line position for parse failures", "import a from", []string{"line 1"}},
		{"returns nil, err instead of raising on the recursion depth limit", strings.Repeat("(", 6000),
			[]string{"max recursion depth"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tree, err := String(c.src)
			if tree != nil || err == nil {
				t.Fatalf("expected parse of %q to fail, got %s", c.src, ast.Tree(tree))
			}
			for _, m := range c.matches {
				if !regexp.MustCompile(m).MatchString(err.Error()) {
					t.Errorf("error %q doesn't match %q", err, m)
				}
			}
		})
	}
}

// regression: Value tries Comprehension before String, so at "[[" the
// parser attempts to parse string content as code; that must not reject
// valid programs
func TestParserSpecCodeLikeLongStrings(t *testing.T) {
	for _, src := range []string{
		"y = [[if x then import a]] .. z",
		"x = [[=[hi]] .. 1",
		`x = y .. [[Flow:extend("]] .. z .. [[")]] .. w`,
	} {
		mustParse(t, src)
	}
}

func TestParserSpecReusableAfterFailure(t *testing.T) {
	if tree, _ := String("x = ("); tree != nil {
		t.Fatal("expected x = ( to fail")
	}
	want := ast.New(ast.NewPos(1, "assign",
		ast.New(ast.NewPos(1, "ref", "x")),
		ast.New(ast.NewPos(4, "number", "1"))))
	if d := sameTree(want, mustParse(t, "x = 1")); d != "" {
		t.Error(d)
	}
}

func TestParserSpecLoopHeaderDo(t *testing.T) {
	// `do` after the while condition is the block keyword, not a do-expression
	tree := mustParse(t, "while x do print 1")
	if k := tree.At(1).(*ast.Node).At(1); k != "while" {
		t.Errorf("got %v, want while", k)
	}
	// but a do-expression is fine in normal expression position
	tree = mustParse(t, "x = do\n  5")
	if k := tree.At(1).(*ast.Node).At(3).(*ast.Node).At(1).(*ast.Node).At(1); k != "do" {
		t.Errorf("got %v, want do", k)
	}
}

func TestParserSpecInterpolatedLuaString(t *testing.T) {
	tree := mustParse(t, `x = "a#{ [[long]] }b"`)
	str := tree.At(1).(*ast.Node).At(3).(*ast.Node).At(1).(*ast.Node)
	inner := str.At(4).(*ast.Node)
	innerStr := inner.At(2).(*ast.Node)
	checks := []struct {
		what      string
		got, want any
	}{
		{"str[1]", str.At(1), "string"},
		{"str[3]", str.At(3), "a"},
		{"str[4][1]", inner.At(1), "interpolate"},
		{"str[4][2][1]", innerStr.At(1), "string"},
		{"str[4][2][2]", innerStr.At(2), "[["},
		{"str[4][2][3]", innerStr.At(3), "long"},
		{"str[5]", str.At(5), "b"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %#v, want %#v", c.what, c.got, c.want)
		}
	}
}
