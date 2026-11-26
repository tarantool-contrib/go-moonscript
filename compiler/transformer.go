package compiler

// Port of moonscript/transform/transformer.moon plus the transformer
// registries.

import "github.com/tarantool-contrib/go-moonscript/ast"

// RetFn is a "cascading" transformation applied to the last statement of a
// body (implicit return, assignment bubbling, ...). It's stored inside
// {"transform", stm, fn} nodes, so it's also a tree value.
type RetFn func(stm any) any

// TransformFn is a transformer entry. node is the node being transformed;
// extra holds the additional Lua arguments (ret function, parent assign...).
// Returning nil means "unchanged" (Lua's `transformer(...) or node`).
type TransformFn func(scope *Block, n *ast.Node, extra ...any) any

// Registries, filled from init() functions:
//
//	statementTransformers: moonscript/transform/statement.moon
//	valueTransformers:     moonscript/transform/value.moon
var (
	statementTransformers = map[string]TransformFn{}
	valueTransformers     = map[string]TransformFn{}
)

type transformer struct {
	fns  map[string]TransformFn
	seen map[*ast.Node]bool
}

func newTransformer(fns map[string]TransformFn) *transformer {
	return &transformer{fns: fns, seen: map[*ast.Node]bool{}}
}

func (t *transformer) lookup(v any) TransformFn {
	n, ok := v.(*ast.Node)
	if !ok {
		// only plain nodes have node types with transformers ("value",
		// "temp_name", "run" have none)
		return nil
	}
	return t.fns[n.Kind()]
}

func (t *transformer) transformOnce(scope *Block, v any, extra ...any) any {
	n, ok := v.(*ast.Node)
	if !ok {
		return v
	}
	if t.seen[n] {
		return n
	}
	t.seen[n] = true
	if fn := t.fns[n.Kind()]; fn != nil {
		if res := fn(scope, n, extra...); res != nil {
			return res
		}
	}
	return n
}

func (t *transformer) transform(scope *Block, v any, extra ...any) any {
	if n, ok := v.(*ast.Node); ok {
		if t.seen[n] {
			return n
		}
		t.seen[n] = true
	} else {
		return v
	}
	cur := v
	for {
		fn := t.lookup(cur)
		res := cur
		if fn != nil {
			if r := fn(scope, cur.(*ast.Node), extra...); r != nil {
				res = r
			}
		}
		if same(res, cur) {
			return cur
		}
		// carry the source position through the transformation
		if isTable(res) && isTable(cur) && posOf(res) == 0 {
			setPos(res, posOf(cur))
		}
		cur = res
	}
}

// same is Lua's == for tree values (identity for tables).
func same(a, b any) bool {
	switch x := a.(type) {
	case *ast.Node:
		y, ok := b.(*ast.Node)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case *NameProxy:
		y, ok := b.(*NameProxy)
		return ok && x == y
	case *LocalName:
		y, ok := b.(*LocalName)
		return ok && x == y
	case *Run:
		y, ok := b.(*Run)
		return ok && x == y
	case int:
		y, ok := b.(int)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case nil:
		return b == nil
	}
	return false
}

// posOf is node[-1] for any table-backed value.
func posOf(v any) int {
	switch t := v.(type) {
	case *ast.Node:
		if t != nil {
			return t.Pos
		}
	case *NameProxy:
		return t.Pos
	case *LocalName:
		return t.Pos
	case *Run:
		return t.Pos
	}
	return 0
}

func setPos(v any, p int) {
	switch t := v.(type) {
	case *ast.Node:
		t.Pos = p
	case *NameProxy:
		t.Pos = p
	case *LocalName:
		t.Pos = p
	case *Run:
		t.Pos = p
	}
}

// --- helpers to read the extra transformer arguments --------------------------

// retArg returns extra[i] as a RetFn (nil when absent, nil or false).
func retArg(extra []any, i int) RetFn {
	if i < len(extra) {
		if fn, ok := extra[i].(RetFn); ok {
			return fn
		}
	}
	return nil
}

func extraArg(extra []any, i int) any {
	if i < len(extra) {
		return extra[i]
	}
	return nil
}
