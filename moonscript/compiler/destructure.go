package compiler

// Port of moonscript/transform/destructure.moon.

import "github.com/tarantool-contrib/go-moonscript/moonscript/ast"

// assignName is one {value, suffix} tuple from extract_assign_names: the
// assign target and the chain items (suffix) that reach it from the
// receiver.
type assignName struct {
	Target any
	Suffix []any
}

// desJoin is join(...): concatenates lists into a new list.
func desJoin(lists ...[]any) []any {
	n := 0
	for _, l := range lists {
		n += len(l)
	}
	out := make([]any, 0, n)
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// hasDestructure: does a list of assign names contain a table literal
func hasDestructure(names *ast.Node) bool {
	for _, n := range names.Items {
		if n == nil {
			break
		}
		if ntype(n) == "table" {
			return true
		}
	}
	return false
}

// desIsMultiReturn: can this value provide more than one return value
func desIsMultiReturn(v any) bool {
	if s, ok := v.(string); ok && s == "..." {
		return true
	}
	n, ok := v.(*ast.Node)
	if !ok || n == nil {
		// non-tables, and table-backed objects ([1] = "temp_name", "run")
		return false
	}
	switch n.Kind() {
	case "chain":
		return ntype(n.Last()) == "call"
	case "explist":
		return true
	}
	return false
}

// extractAssignNames is destructure.extract_assign_names(name).
func extractAssignNames(name *ast.Node) []assignName {
	return desExtractAssignNames(name, nil, nil)
}

func desExtractAssignNames(name *ast.Node, accum []assignName, prefix []any) []assignName {
	i := 1
	tuples := asNode(name.At(2))
	for _, t := range tuples.Items {
		if t == nil {
			break
		}
		tuple := t.(*ast.Node)
		var value, suffix any
		if tuple.Len() == 1 {
			suffix = node("index", node("number", i))
			i++
			value = tuple.At(1)
		} else {
			key := tuple.At(1)
			if ntype(key) == "key_literal" {
				keyName := key.(*ast.Node).At(2)
				if ntype(keyName) == "colon" {
					suffix = keyName
				} else {
					suffix = node("dot", keyName)
				}
			} else {
				suffix = node("index", key)
			}
			value = tuple.At(2)
		}

		full := desJoin(prefix, []any{suffix})

		if ntype(value) == "table" {
			accum = desExtractAssignNames(value.(*ast.Node), accum, full)
		} else if isAssignable(value) {
			accum = append(accum, assignName{Target: value, Suffix: full})
		} else {
			pos := 0
			if isTable(value) {
				pos = posOf(value)
			}
			if pos == 0 {
				pos = name.Pos
			}
			if s, ok := value.(string); ok && s == "..." {
				userError("Can't destructure into '...'", pos)
			} else if ntype(value) == "chain" {
				userError("Can't destructure into chain ending in "+ntype(value.(*ast.Node).Last()), pos)
			} else {
				userError("Can't destructure value of type: "+ntype(value), pos)
			}
		}
	}
	return accum
}

// can the receiver be referenced directly as the base of a chain? anything
// not recognized here is copied to a temporary name before destructuring.
// exp, table and string are chainable because the compiler wraps them in
// parentheses
var desKeywordRefs = set("nil", "true", "false")

func desChainableReceiver(v any) bool {
	switch t := v.(type) {
	case string:
		// legacy transformers use plain strings for names
		return t != "..." && !desKeywordRefs[t]
	case *ast.Node:
		if t == nil {
			return false
		}
		switch t.Kind() {
		case "ref":
			s, _ := t.At(2).(string)
			return !desKeywordRefs[s]
		case "chain", "self", "self_class", "temp_name", "parens", "exp", "table", "string":
			return true
		}
		return false
	case ast.Typed:
		return t.NType() == "temp_name"
	}
	return false
}

// buildDestructureAssign is destructure.build_assign(scope, literal,
// receiver, opts); shadow is opts.shadow.
func buildDestructureAssign(scope *Block, literal *ast.Node, receiver any, shadow bool) *ast.Node {
	if !truthy(receiver) {
		panic("attempting to build destructure assign with no receiver")
	}

	extracted := extractAssignNames(literal)

	names := node()
	values := node()

	inner := node("assign", names, values)

	var obj any
	if desChainableReceiver(receiver) && (scope.IsLocal(receiver) || len(extracted) == 1) {
		obj = receiver
	} else {
		proxy := NewNameProxy("obj")
		obj = proxy
		inner = buildDo(node(
			buildAssignOne(proxy, receiver),
			node("assign", names, values),
		))
	}

	for _, tuple := range extracted {
		names.Append(tuple.Target)
		values.Append(proxyChain(obj, tuple.Suffix...))
	}

	group := node()

	if shadow {
		// force new locals so the targets never assign to an enclosing
		// scope, only plain names can be declared (chain and self targets
		// can't)
		shadowNames := node()
		for _, name := range names.Items {
			if _, isStr := name.(string); ntype(name) == "ref" || isStr {
				shadowNames.Append(name)
			}
		}
		if shadowNames.Len() > 0 {
			group.Append(node("declare_with_shadows", shadowNames))
		}
	} else {
		group.Append(node("declare", names))
	}

	group.Append(inner)
	return buildGroup(group)
}

// desRange is the comprehension [list[j] for j=from,to]. Lua leaves holes
// for nil items; they only occur on paths that error out afterwards.
func desRange(list *ast.Node, from, to int) *ast.Node {
	out := node()
	for j := from; j <= to; j++ {
		out.Append(list.At(j))
	}
	return out
}

// splitAssign is destructure.split_assign(scope, assign): applies
// destructuring to an assign node.
func splitAssign(scope *Block, assign *ast.Node) *ast.Node {
	names := asNode(assign.At(2))
	values := asNode(assign.At(3))

	g := node()
	totalNames := names.Len()
	totalValues := values.Len()

	// if the final value can return multiple values then the names
	// consuming its returns must be assigned in a single statement.
	// destructuring literals among them receive a temporary that is unpacked
	// afterwards
	suffixStart := 0
	if totalNames > totalValues && desIsMultiReturn(values.At(totalValues)) {
		for i := totalValues; i <= totalNames; i++ {
			if ntype(names.At(i)) == "table" {
				suffixStart = totalValues
				break
			}
		}
	}

	// We have to break apart the assign into groups of regular assigns, and
	// then the destructuring assignments
	start := 1
	stop := totalNames
	if suffixStart != 0 {
		stop = suffixStart - 1
	}
	for i := 1; i <= stop; i++ {
		n := names.At(i)
		if ntype(n) == "table" {
			if i > start {
				g.Append(node(
					"assign",
					desRange(names, start, i-1),
					desRange(values, start, i-1),
				))
			}

			g.Append(buildDestructureAssign(scope, n.(*ast.Node), values.At(i), false))

			start = i + 1
		}
	}

	if suffixStart != 0 {
		// plain names left over between the last destructure and the suffix
		if start <= stop {
			g.Append(node(
				"assign",
				desRange(names, start, stop),
				desRange(values, start, stop),
			))
		}

		suffixNames := node()
		type destructure struct {
			literal *ast.Node
			proxy   *NameProxy
		}
		var destructures []destructure
		for i := suffixStart; i <= totalNames; i++ {
			name := names.At(i)
			if ntype(name) == "table" {
				proxy := NewNameProxy("destruct")
				suffixNames.Append(proxy)
				destructures = append(destructures, destructure{name.(*ast.Node), proxy})
			} else {
				suffixNames.Append(name)
			}
		}

		g.Append(node("assign", suffixNames, node(values.At(totalValues))))

		for _, d := range destructures {
			g.Append(buildDestructureAssign(scope, d.literal, d.proxy, false))
		}
	} else if totalNames >= start || totalValues >= start {
		var nameSlice, valueSlice *ast.Node
		if totalNames < start {
			nameSlice = node(scope.DiscardName())
		} else {
			nameSlice = desRange(names, start, totalNames)
		}

		if totalValues < start {
			valueSlice = node("nil")
		} else {
			valueSlice = desRange(values, start, totalValues)
		}

		g.Append(node("assign", nameSlice, valueSlice))
	}

	return buildGroup(g)
}
