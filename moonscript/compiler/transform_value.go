package compiler

// Port of moonscript/transform/value.moon.

import "github.com/tarantool-contrib/go-moonscript/moonscript/ast"

func init() {
	for name, fn := range map[string]TransformFn{
		"for":     defaultAccumulator,
		"while":   defaultAccumulator,
		"foreach": defaultAccumulator,

		"do": func(scope *Block, n *ast.Node, _ ...any) any {
			return buildBlockExp(asNode(n.At(2)))
		},

		"decorated": func(scope *Block, n *ast.Node, _ ...any) any {
			return scope.TransformStatement(n)
		},

		"class":  valWrapBlockExp,
		"if":     valWrapBlockExp,
		"unless": valWrapBlockExp,
		"with":   valWrapBlockExp,
		"switch": valWrapBlockExp,

		"string":           valString,
		"comprehension":    valComprehension,
		"tblcomprehension": valTblComprehension,
		"fndef":            valFndef,
		"chain":            valChain,
		"block_exp":        valBlockExp,
	} {
		valueTransformers[name] = fn
	}
}

// valList is the Lua table constructor {v}: a nil v gives an empty list.
func valList(v any) *ast.Node {
	if v == nil {
		return node()
	}
	return node(v)
}

func valWrapBlockExp(scope *Block, n *ast.Node, _ ...any) any {
	return buildBlockExp(node(n))
}

func valString(scope *Block, n *ast.Node, _ ...any) any {
	delim := n.At(2)

	convertPart := func(part any) any {
		if s, ok := part.(string); ok {
			return node("string", delim, s)
		}
		if part == nil {
			return node("string", delim, "")
		}
		return buildChain("tostring", node("call", valList(part.(*ast.Node).At(2))))
	}

	// reduced to single item
	if n.Len() <= 3 {
		if _, ok := n.At(3).(string); ok {
			return n
		}
		return convertPart(n.At(3))
	}

	e := node("exp", convertPart(n.At(3)))

	for i := 4; i <= n.Len(); i++ {
		e.Append("..", convertPart(n.At(i)))
	}
	return e
}

func valComprehension(scope *Block, n *ast.Node, _ ...any) any {
	a := newAccumulator()
	res := scope.TransformStatement(n, RetFn(func(exp any) any {
		return a.mutateBody(valList(exp))
	}))
	return a.wrap(res, "block_exp")
}

func valTblComprehension(scope *Block, n *ast.Node, _ ...any) any {
	explist, clauses := asNode(n.At(2)), asNode(n.At(3))
	keyExp, valueExp := explist.At(1), explist.At(2)

	accum := NewNameProxy("tbl")

	var inner *ast.Node
	if truthy(valueExp) {
		dest := buildChain(accum, node("index", keyExp))
		inner = node(buildAssignOne(dest, valueExp))
	} else {
		// If we only have single expression then unpack the result into key
		// and value
		keyName, valName := NewNameProxy("key"), NewNameProxy("val")
		dest := buildChain(accum, node("index", keyName))
		inner = node(
			buildAssign(node(keyName, valName), valList(keyExp)),
			buildAssignOne(dest, valName),
		)
	}

	return buildBlockExp(node(
		buildAssignOne(accum, buildTable()),
		constructComprehension(inner, clauses),
		accum,
	))
}

// fndef smart_node fields: args=2, whitelist=3, arrow=4, body=5
func valFndef(scope *Block, n *ast.Node, _ ...any) any {
	n.Set(5, transformLastStm(asNode(n.At(5)), implicitlyReturn(scope)))

	args := asNode(n.At(2))

	// from the first destructuring arg onward, argument initialization
	// (default checks, self assigns, unpacking) moves into the body in left
	// to right order, so a later default can reference an earlier
	// destructured name:
	//   f = ({:a}, b = a) -> b
	// earlier args stay on the compiler's default handling, keeping output
	// for functions without destructuring unchanged
	firstDestructure := 0
	for i, arg := range args.Items {
		if arg == nil {
			break
		}
		if ntype(asNode(arg).At(1)) == "table" {
			firstDestructure = i + 1
			break
		}
	}

	if firstDestructure == 0 {
		return n
	}

	// a destructured name always shadows at the top of the body, so a later
	// parameter of the same name could never be seen. reject the duplicate
	// instead of silently breaking later-parameter-wins
	boundNames := map[string]bool{}
	// fat arrow binds self as an implicit first parameter
	if n.At(4) == "fat" {
		boundNames["self"] = true
	}
	for _, a := range args.Items {
		if a == nil {
			break
		}
		name := asNode(a).At(1)
		switch ntype(name) {
		case "self", "self_class":
			if s, ok := name.(*ast.Node).At(2).(string); ok {
				boundNames[s] = true
			}
		case "table":
		default:
			if s, ok := name.(string); ok {
				boundNames[s] = true
			}
		}
	}

	seenTargets := map[string]bool{}
	for _, a := range args.Items {
		if a == nil {
			break
		}
		name := asNode(a).At(1)
		if ntype(name) != "table" {
			continue
		}
		var targets []*ast.Node
		for _, t := range extractAssignNames(name.(*ast.Node)) {
			if ntype(t.Target) == "ref" {
				targets = append(targets, t.Target.(*ast.Node))
			}
		}

		for _, target := range targets {
			tname, _ := target.At(2).(string)
			if boundNames[tname] || seenTargets[tname] {
				userError("Can't destructure into '"+tname+"': name is bound by another parameter", target.Pos)
			}
		}

		for _, target := range targets {
			tname, _ := target.At(2).(string)
			seenTargets[tname] = true
		}
	}

	defaultCheck := func(name, value any) *ast.Node {
		return node("if", node("exp", name, "==", "nil"), node(node("assign", node(name), node(value))))
	}

	prelude := node()
	for i := firstDestructure; i <= args.Len(); i++ {
		arg := asNode(args.At(i))
		name, defaultValue := arg.At(1), arg.At(2)

		switch ntype(name) {
		case "table":
			proxy := NewNameProxy("arg")
			if truthy(defaultValue) {
				prelude.Append(defaultCheck(proxy, defaultValue))
			}
			prelude.Append(buildDestructureAssign(scope, name.(*ast.Node), proxy, true))
			args.Set(i, node(proxy))
		case "self", "self_class":
			rawName := name.(*ast.Node).At(2)
			if truthy(defaultValue) {
				prelude.Append(defaultCheck(node("ref", rawName), defaultValue))
			}
			prelude.Append(buildAssignOne(name, node("ref", rawName)))
			args.Set(i, node(rawName))
		default:
			if truthy(defaultValue) {
				prelude.Append(defaultCheck(node("ref", name), defaultValue))
				args.Set(i, node(name))
			}
		}
	}

	prelude.Append(buildGroup(asNode(n.At(5))))
	n.Set(5, prelude)

	return n
}

// pull out colon chain
func valChain(scope *Block, n *ast.Node, _ ...any) any {
	// escape lua keywords used in dot accessors
	for i := 2; i <= n.Len(); i++ {
		part := n.At(i)
		if ntype(part) == "dot" {
			if s, ok := part.(*ast.Node).At(2).(string); ok && luaKeywords[s] {
				n.Set(i, node("index", node("string", `"`, s)))
			}
		}
	}

	if ntype(n.At(2)) == "string" {
		// add parens if callee is raw string
		n.Set(2, node("parens", n.At(2)))
	}

	if !chainIsStub(n) {
		return nil
	}

	baseName := NewNameProxy("base")
	fnName := NewNameProxy("fn")
	colon := asNode(n.Remove())

	// a stub with no base takes the scope from the enclosing with block
	if !truthy(n.At(2)) {
		scopeVar := scope.Get("scope_var")
		if !truthy(scopeVar) {
			userError("Short-colon syntax must be called within a with block", n.Pos)
		}
		n.Set(2, scopeVar)
	}

	base := n.At(2)
	isSuper := ntype(base) == "ref" && base.(*ast.Node).At(2) == "super"
	var self any = baseName
	if isSuper {
		self = "self"
	}

	return buildBlockExp(node(
		buildAssign(node(baseName), node(n)),
		buildAssign(node(fnName), node(buildChain(baseName, node("dot", colon.At(2))))),
		buildFndef(node(node("...")), nil, "", node(
			buildChain(fnName, node("call", node(self, "..."))),
		)),
	))
}

func valBlockExp(scope *Block, n *ast.Node, _ ...any) any {
	body := asNode(n.At(2))

	argList := node()
	fn := buildFndef(nil, nil, "", body)

	// if the body references varargs then the wrapper function must accept
	// and forward them so they remain visible
	if hasVarargs(fn.At(5).(*ast.Node)) {
		argList.Append("...")
		fn.At(2).(*ast.Node).Append(node("..."))
	}

	return buildChain(node("parens", fn), node("call", argList))
}
