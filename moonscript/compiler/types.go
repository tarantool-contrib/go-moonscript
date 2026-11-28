// Package compiler is a port of the MoonScript v0.8.0 transformer and Lua
// code generator (moonscript/transform/*, moonscript/compile/*).
//
// Porting conventions (read before editing):
//
//   - The tree is made of *ast.Node values that behave like Lua tables:
//     n.At(i) is node[i] with Lua's 1-based indices, n.Len() is #node,
//     n.Pos is node[-1]. Values inside nodes are nil, string, bool, int,
//     *ast.Node, or table-backed compiler objects (*NameProxy, *LocalName,
//     *Run) and RetFn closures stored in {"transform", stm, fn} nodes.
//   - ntype(v) is types.ntype. isTable(v) is type(v) == "table".
//   - smart_node field names map to indices: class{name=2, body=3}
//     fndef{args=2, whitelist=3, arrow=4, body=5} foreach{names=2, iter=3,
//     body=4} for{name=2, bounds=3, body=4} while{cond=2, body=3}
//     assign{names=2, values=3} declare{names=2} if{cond=2, then=3}.
//   - Lua `error {"user-error", msg, pos}` is userError(msg, pos) (a panic
//     recovered by Tree); plain `error "msg"` is panic(msg).
//   - Transformers are registered from init() functions into
//     statementTransformers / valueTransformers; see transformer.go.
package compiler

import (
	"unicode"

	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
)

func ntype(v any) string { return ast.NType(v) }

func isTable(v any) bool { return ast.IsTable(v) }

func node(items ...any) *ast.Node { return ast.New(items...) }

func asNode(v any) *ast.Node { return ast.AsNode(v) }

func truthy(v any) bool { return ast.Truthy(v) }

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// implicit return does not work on these statements
var manualReturn = set("foreach", "for", "while", "return")

// Assigns and returns are bubbled into their bodies. All cascading statement
// transform functions accept a second argument that is the transformation to
// apply to the last statement in their body.
var cascading = set("if", "unless", "with", "switch", "class", "do")

var terminating = set("return", "break")

var luaKeywords = set(
	"and", "break", "do", "else", "elseif",
	"end", "false", "for", "function", "if",
	"in", "local", "nil", "not", "or",
	"repeat", "return", "then", "true",
	"until", "while",
)

// valueCanBeStatement: can this value be compiled in a line by itself
func valueCanBeStatement(n any) bool {
	if ntype(n) != "chain" {
		return false
	}
	return ntype(n.(*ast.Node).Last()) == "call"
}

// isValue is types.is_value: compile.Block\is_value(stm) or
// transform.Value\can_transform(stm).
func isValue(stm any) bool {
	return blockIsValue(stm) || valueTransformers[ntype(stm)] != nil
}

// blockIsValue is the class-level Block\is_value.
func blockIsValue(n any) bool {
	t := ntype(n)
	return valueCompilers[t] != nil || t == "value"
}

var chainAssignable = set("index", "dot", "slice")

// isAssignable determines if node is able to be on left side of assignment
func isAssignable(n any) bool {
	if s, ok := n.(string); ok && s == "..." {
		return false
	}
	switch ntype(n) {
	case "ref", "self", "value", "self_class", "table":
		return true
	case "chain":
		return chainAssignable[ntype(n.(*ast.Node).Last())]
	}
	return false
}

// valueIsSingular: is this expression a single term that can sit next to a
// binary operator without parentheses changing its meaning
func valueIsSingular(v any) bool {
	n, ok := v.(*ast.Node)
	if !ok {
		// non-table values, and table-backed objects whose [1] isn't a node
		// type handled below
		return true
	}
	switch n.Kind() {
	case "exp":
		return n.Len() == 2 && valueIsSingular(n.At(2))
	case "length", "minus", "not", "bitnot":
		return valueIsSingular(n.At(2))
	}
	return true
}

func isSlice(n any) bool {
	return ntype(n) == "chain" && ntype(n.(*ast.Node).Last()) == "slice"
}

// matchUpper is Lua's name:match "^%u"
func matchUpper(s string) bool {
	return s != "" && s[0] < 0x80 && unicode.IsUpper(rune(s[0]))
}

// noop returns a {"noop"} node. The reference shares one NOOP table; nothing
// depends on its identity, so a fresh node is used to avoid shared mutation.
func noop() *ast.Node { return node("noop") }

// --- build helpers (types.build) ---------------------------------------------

func buildGroup(body *ast.Node) *ast.Node {
	if body == nil {
		body = node()
	}
	return node("group", body)
}

func buildDo(body *ast.Node) *ast.Node { return node("do", body) }

func buildBlockExp(body *ast.Node) *ast.Node { return node("block_exp", body) }

func buildAssign(names, values *ast.Node) *ast.Node {
	if names == nil {
		names = node()
	}
	if values == nil {
		values = node()
	}
	return node("assign", names, values)
}

func buildAssignOne(name, value any) *ast.Node {
	return buildAssign(node(name), node(value))
}

func buildDeclare(names *ast.Node) *ast.Node {
	if names == nil {
		names = node()
	}
	return node("declare", names)
}

// buildTable converts string keys to key literals (mutating the tuples).
// nil tuples are skipped (they'd be holes at the end of the Lua table).
func buildTable(tuples ...*ast.Node) *ast.Node {
	tbl := node()
	for _, tuple := range tuples {
		if tuple == nil {
			continue
		}
		if s, ok := tuple.At(1).(string); ok {
			tuple.Set(1, node("key_literal", s))
		}
		tbl.Append(tuple)
	}
	return node("table", tbl)
}

// buildChain is build.chain {base: base, parts...}; a string base becomes a
// ref.
func buildChain(base any, parts ...any) *ast.Node {
	if s, ok := base.(string); ok {
		base = node("ref", s)
	}
	n := node("chain", base)
	n.Append(parts...)
	return n
}

// buildFndef is build.fndef {args, whitelist, arrow, body}; nil/"" take the
// defaults ({}, {}, "slim", {}).
func buildFndef(args, whitelist *ast.Node, arrow string, body *ast.Node) *ast.Node {
	if args == nil {
		args = node()
	}
	if whitelist == nil {
		whitelist = node()
	}
	if arrow == "" {
		arrow = "slim"
	}
	if body == nil {
		body = node()
	}
	return node("fndef", args, whitelist, arrow, body)
}

func buildIf(cond any, then *ast.Node) *ast.Node {
	if cond == nil {
		cond = node()
	}
	if then == nil {
		then = node()
	}
	return node("if", cond, then)
}

func buildFor(name any, bounds, body *ast.Node) *ast.Node {
	if bounds == nil {
		bounds = node()
	}
	if body == nil {
		body = node()
	}
	return node("for", name, bounds, body)
}
