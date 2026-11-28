package compiler

// Port of upstream v0.8.0 spec/compiler_spec.moon: raw Lua-AST nodes
// compiled by a block with no transform step (the spec's SimpleBlock).

import (
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
)

// newSimpleBlock is the spec's SimpleBlock: a Block whose value and
// statement transformers return their input unchanged.
func newSimpleBlock() *Block {
	b := NewRootBlock(Options{})
	b.stmT = newTransformer(map[string]TransformFn{})
	b.valT = newTransformer(map[string]TransformFn{})
	return b
}

// compileNode is the spec's compile_node.
func compileNode(n any) (out string, err any) {
	defer func() {
		if r := recover(); r != nil {
			err = r
		}
	}()
	b := newSimpleBlock()
	b.Add(b.Value(n))
	lines := b.lines.Flatten(nil, nil)
	if k := len(lines); k > 0 && lines[k-1] == "\n" {
		lines = lines[:k-1]
	}
	return strings.Join(lines, ""), nil
}

// spec/factory.moon
func specRef(name ...string) *ast.Node {
	if len(name) == 0 {
		return ast.New("ref", "val")
	}
	return ast.New("ref", name[0])
}

func specStr() *ast.Node { return ast.New("string", `"`, "dogzone") }

func TestCompilerSpecValue(t *testing.T) {
	n := ast.New
	ref := specRef
	cases := []struct {
		name string
		node func() any
		want string
	}{
		{"ref", func() any { return n("ref", "hello_world") }, "hello_world"},
		{"number", func() any { return n("number", "14") }, "14"},
		{"minus", func() any { return n("minus", ref()) }, "-val"},
		{"explist", func() any { return n("explist", ref("a"), ref("b"), ref("c")) }, "a, b, c"},
		{"exp", func() any { return n("exp", ref("a"), "+", ref("b"), "!=", ref("c")) }, "a + b ~= c"},
		{"exp (nested under tighter operator)", func() any {
			return n("exp", n("exp", ref("a"), "..", ref("b")), "*", ref("c"))
		}, "(a .. b) * c"},
		{"exp (nested under looser operator)", func() any {
			return n("exp", ref("a"), "==", n("exp", ref("b"), "..", ref("c")))
		}, "a == b .. c"},
		{"exp (nested right of equal precedence left associative)", func() any {
			return n("exp", ref("a"), "-", n("exp", ref("b"), "+", ref("c")))
		}, "a - (b + c)"},
		{"exp (nested concat left of concat keeps grouping)", func() any {
			return n("exp", n("exp", ref("a"), "..", ref("b")), "..", ref("c"))
		}, "(a .. b) .. c"},
		{"exp (nested concat right of concat stays flat)", func() any {
			return n("exp", ref("a"), "..", n("exp", ref("b"), "..", ref("c")))
		}, "a .. b .. c"},
		{"exp (nested left of right associative operator)", func() any {
			return n("exp", n("exp", ref("x"), "^", ref("y")), "^", ref("b"))
		}, "(x ^ y) ^ b"},
		{"parens", func() any { return n("parens", ref()) }, "(val)"},
		{"string (single quote)", func() any { return n("string", "'", `Hello\'s world`) }, `'Hello\'s world'`},
		{"string (double quote)", func() any { return n("string", `"`, "Hello's world") }, `"Hello's world"`},
		{"string (lua)", func() any { return n("string", "[==[", "Hello's world") }, "[==[Hello's world]==]"},
		{"self", func() any { return n("self", ref()) }, "self.val"},
		{"self_class", func() any { return n("self_class", ref()) }, "self.__class.val"},
		{"self_class_colon", func() any { return n("self_class_colon", ref()) }, "self.__class:val"},
		{"not", func() any { return n("not", ref()) }, "not val"},
		{"length", func() any { return n("length", ref()) }, "#val"},
		{"length", func() any { return n("length", ref()) }, "#val"},
		{"bitnot", func() any { return n("bitnot", ref()) }, "~val"},
		{"chain (single)", func() any { return n("chain", ref()) }, "val"},
		{"chain (dot)", func() any { return n("chain", ref(), n("dot", "zone")) }, "val.zone"},
		{"chain (index)", func() any { return n("chain", ref(), n("index", ref("x"))) }, "val[x]"},
		{"chain (call)", func() any { return n("chain", ref(), n("call", n(ref("arg")))) }, "val(arg)"},
		{"chain", func() any {
			return n("chain", ref(), n("dot", "one"), n("index", specStr()), n("colon", "two"), n("call", n(ref("arg"))))
		}, `val.one["dogzone"]:two(arg)`},
		{"chain (self receiver)", func() any {
			return n("chain", n("self", ref()), n("call", n(ref("arg"))))
		}, "self:val(arg)"},
		{"fndef (empty)", func() any { return n("fndef", n(), n(), "slim", n()) }, "function() end"},
	}
	for _, c := range cases {
		t.Run("compiles "+c.name, func(t *testing.T) {
			got, err := compileNode(c.node())
			if err != nil {
				t.Fatalf("compile failed: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
