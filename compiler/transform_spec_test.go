package compiler

// Port of upstream v0.8.0 spec/transform_spec.moon:
// moonscript.transform.destructure (split_assign, extract_assign_names) and
// moonscript.transform.statements (last_stm, transform_last_stm), compared
// node by node like busted's assert.same.

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/ast"
)

// specProxy stands for a NameProxy in an expected tree: the spec's
// {"temp_name", prefix: "obj"} table.
type specProxy struct{ prefix string }

// luaSame is assert.same for compiler trees: nodes compare item by item
// (Pos is the [-1] slot), NameProxy against specProxy by prefix, functions
// and compiler objects by identity. It returns a description of the first
// difference.
func luaSame(want, got any) string {
	switch w := want.(type) {
	case *ast.Node:
		g, ok := got.(*ast.Node)
		if !ok || g == nil {
			return fmt.Sprintf("want %s, got %s", ast.FlatValue(w), ast.FlatValue(got))
		}
		if w.Pos != g.Pos {
			return fmt.Sprintf("pos: want %d, got %d in %s", w.Pos, g.Pos, ast.FlatValue(g))
		}
		if w.Len() != g.Len() {
			return fmt.Sprintf("length: want %s, got %s", ast.FlatValue(w), ast.FlatValue(g))
		}
		for i := range w.Items {
			if d := luaSame(w.Items[i], g.Items[i]); d != "" {
				return fmt.Sprintf("[%d] %s", i+1, d)
			}
		}
		return ""
	case specProxy:
		g, ok := got.(*NameProxy)
		if !ok {
			return fmt.Sprintf("want temp_name proxy, got %s", ast.FlatValue(got))
		}
		if g.Prefix != w.prefix || g.named {
			return fmt.Sprintf("want proxy prefix %q, got %s", w.prefix, g)
		}
		return ""
	case RetFn:
		g, ok := got.(RetFn)
		if !ok || reflect.ValueOf(g).Pointer() != reflect.ValueOf(w).Pointer() {
			return "want the same transformer function"
		}
		return ""
	case *Run, *NameProxy:
		if want != got {
			return fmt.Sprintf("want the same object %v, got %v", want, got)
		}
		return ""
	}
	if want != got {
		return fmt.Sprintf("want %s, got %s", ast.FlatValue(want), ast.FlatValue(got))
	}
	return ""
}

func assertSame(t *testing.T, want, got any) {
	t.Helper()
	if d := luaSame(want, got); d != "" {
		t.Errorf("not same: %s\nwant %s\ngot  %s", d, ast.FlatValue(want), ast.FlatValue(got))
	}
}

func TestTransformSpecSplitAssign(t *testing.T) {
	n := ast.New
	ref := func(s string) *ast.Node { return n("ref", s) }

	// {:hello} = world
	t.Run("simple assignment", func(t *testing.T) {
		node := n("assign",
			n(n("table", n(n(n("key_literal", "hello"), ref("hello"))))),
			n(ref("world")))
		out := splitAssign(NewRootBlock(Options{}), node)
		assertSame(t, n("group", n(
			n("group", n(
				n("declare", n(ref("hello"))),
				n("assign", n(ref("hello")), n(n("chain", ref("world"), n("dot", "hello")))),
			)),
		)), out)
	})

	// {:a, :b} = world!
	// a complex value should never be repeated to avoid double execution
	t.Run("complex value", func(t *testing.T) {
		node := n("assign",
			n(n("table", n(
				n(n("key_literal", "a"), ref("a")),
				n(n("key_literal", "b"), ref("b")),
			))),
			n(n("chain", ref("world"), n("call", n()))))
		out := splitAssign(NewRootBlock(Options{}), node)

		// the temp name the result is stored into
		tmp := specProxy{"obj"}
		assertSame(t, n("group", n(
			n("group", n(
				n("declare", n(ref("a"), ref("b"))),
				n("do", n(
					n("assign", n(tmp), n(n("chain", ref("world"), n("call", n())))),
					n("assign", n(ref("a"), ref("b")), n(n("chain", tmp, n("dot", "a")), n("chain", tmp, n("dot", "b")))),
				)),
			)),
		)), out)
	})

	// a, {:hello} = one, two
	t.Run("multiple assigns", func(t *testing.T) {
		node := n("assign",
			n(ref("a"), n("table", n(n(n("key_literal", "hello"), ref("hello"))))),
			n(ref("one"), ref("two")))
		out := splitAssign(NewRootBlock(Options{}), node)
		assertSame(t, n("group", n(
			n("assign", n(ref("a")), n(ref("one"))),
			n("group", n(
				n("declare", n(ref("hello"))),
				n("assign", n(ref("hello")), n(n("chain", ref("two"), n("dot", "hello")))),
			)),
		)), out)
	})

	// {:hello}, a = one, two
	t.Run("multiple assigns swapped", func(t *testing.T) {
		node := n("assign",
			n(n("table", n(n(n("key_literal", "hello"), ref("hello")))), ref("a")),
			n(ref("one"), ref("two")))
		out := splitAssign(NewRootBlock(Options{}), node)
		assertSame(t, n("group", n(
			n("group", n(
				n("declare", n(ref("hello"))),
				n("assign", n(ref("hello")), n(n("chain", ref("one"), n("dot", "hello")))),
			)),
			n("assign", n(ref("a")), n(ref("two"))),
		)), out)
	})

	// a, {:hello} = func! -- where func returns multiple values, second destructured
	t.Run("multiple return value", func(t *testing.T) {
		node := n("assign",
			n(ref("a"), n("table", n(n(n("key_literal", "hello"), ref("hello"))))),
			n(n("chain", ref("func"), n("call", n()))))
		out := splitAssign(NewRootBlock(Options{}), node)

		assertSame(t, "group", out.At(1))
		body := out.At(2).(*ast.Node)
		assertSame(t, 2, body.Len())

		assign := body.At(1).(*ast.Node)
		assertSame(t, "assign", assign.At(1))
		assertSame(t, ref("a"), assign.At(2).(*ast.Node).At(1))
		tmp := assign.At(2).(*ast.Node).At(2)
		assertSame(t, "temp_name", ntype(tmp))
		assertSame(t, n(n("chain", ref("func"), n("call", n()))), assign.At(3))

		destructGroup := body.At(2).(*ast.Node)
		assertSame(t, "group", destructGroup.At(1))
		declare := destructGroup.At(2).(*ast.Node).At(1)
		assertSame(t, n("declare", n(ref("hello"))), declare)

		destructAssign := destructGroup.At(2).(*ast.Node).At(2).(*ast.Node)
		assertSame(t, "assign", destructAssign.At(1))
		assertSame(t, n(ref("hello")), destructAssign.At(2))

		destructValue := destructAssign.At(3).(*ast.Node).At(1).(*ast.Node)
		assertSame(t, "chain", destructValue.At(1))
		if tmp != destructValue.At(2) {
			t.Errorf("destructured value isn't chained off the temp name: %s", ast.FlatValue(destructValue))
		}
		assertSame(t, "dot", destructValue.At(3).(*ast.Node).At(1))
		assertSame(t, "hello", destructValue.At(3).(*ast.Node).At(2))
	})
}

// assignNamesNode turns extract_assign_names output into the spec's
// {{target, {suffix...}}, ...} shape.
func assignNamesNode(names []assignName) *ast.Node {
	out := ast.New()
	for _, a := range names {
		out.Append(ast.New(a.Target, ast.New(a.Suffix...)))
	}
	return out
}

func TestTransformSpecExtractAssignNames(t *testing.T) {
	n := ast.New
	t.Run("extracts names from table destructure", func(t *testing.T) {
		des := n("table", n(
			n(n("key_literal", "hi"), n("ref", "hi")),
			n(n("key_literal", "world"), n("ref", "world")),
		))
		assertSame(t, n(
			n(n("ref", "hi"), n(n("dot", "hi"))),
			n(n("ref", "world"), n(n("dot", "world"))),
		), assignNamesNode(extractAssignNames(des)))
	})

	t.Run("extracts names from array destructure", func(t *testing.T) {
		des := n("table", n(n(n("ref", "hi"))))
		assertSame(t, n(
			n(n("ref", "hi"), n(n("index", n("number", 1)))),
		), assignNamesNode(extractAssignNames(des)))
	})
}

func TestTransformSpecLastStm(t *testing.T) {
	n := ast.New
	run := func() *Run { return NewRun(func(*Block) {}) } // Run => print "hi"

	t.Run("gets last statement from empty list", func(t *testing.T) {
		stm, _, _ := lastStm(n())
		if stm != nil {
			t.Errorf("got %v, want nil", stm)
		}
	})

	t.Run("gets last statement", func(t *testing.T) {
		stms := n(n("ref", "butt_world"), n("ref", "hello_world"))
		stm, idx, tbl := lastStm(stms)
		if stm != stms.At(2) || idx != 2 || tbl != stms {
			t.Errorf("got %v, %d, %v", stm, idx, tbl)
		}
	})

	t.Run("gets last statement ignoring run", func(t *testing.T) {
		stms := n(n("ref", "butt_world"), n("ref", "hello_world"), run())
		stm, idx, tbl := lastStm(stms)
		if stm != stms.At(2) || idx != 2 || tbl != stms {
			t.Errorf("got %v, %d, %v", stm, idx, tbl)
		}
	})

	t.Run("gets last from within group", func(t *testing.T) {
		stms := n(n("ref", "butt_world"), n("group", n(n("ref", "hello_world"), n("ref", "cool_world"))))
		inner := stms.At(2).(*ast.Node).At(2).(*ast.Node)
		last := inner.At(2)
		stm, idx, tbl := lastStm(stms)
		if stm != last {
			t.Error("should get last")
		}
		if idx != 2 {
			t.Errorf("idx = %d, want 2", idx)
		}
		if tbl != inner {
			t.Error("should get correct table")
		}
	})
}

func TestTransformSpecTransformLastStm(t *testing.T) {
	n := ast.New

	t.Run("transforms empty stms", func(t *testing.T) {
		before := n()
		after := transformLastStm(before, func(stm any) any { return n("wrapped", stm) })
		assertSame(t, before, after)
		if before == after {
			t.Error("expected a new list")
		}
	})

	t.Run("transforms stms", func(t *testing.T) {
		before := n(n("ref", "butt_world"), n("ref", "hello_world"))
		transformer := RetFn(func(stm any) any { return stm })
		after := transformLastStm(before, transformer)
		assertSame(t, n(
			n("ref", "butt_world"),
			n("transform", n("ref", "hello_world"), transformer),
		), after)
	})

	t.Run("transforms empty stms ignoring runs", func(t *testing.T) {
		before := n(n("ref", "butt_world"), n("ref", "hello_world"), NewRun(func(*Block) {}))
		transformer := RetFn(func(stm any) any { return stm })
		after := transformLastStm(before, transformer)
		assertSame(t, n(
			n("ref", "butt_world"),
			n("transform", n("ref", "hello_world"), transformer),
			before.At(3),
		), after)
	})
}
