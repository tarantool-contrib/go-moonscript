package compiler

// Port of moonscript/transform/statement.moon (the class transformer lives in
// class.go).

import (
	"strings"

	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
)

// stmCallRet calls a cascading/action function found in the tree or in the
// extra transformer arguments.
func stmCallRet(fn any, v any) any {
	switch f := fn.(type) {
	case RetFn:
		return f(v)
	case func(any) any:
		return f(v)
	}
	panic("attempt to call a non-function value")
}

// stmIsFn reports whether v is a callable (truthy) ret/action argument.
func stmIsFn(v any) bool {
	switch f := v.(type) {
	case RetFn:
		return f != nil
	case func(any) any:
		return f != nil
	}
	return false
}

// stmRetFn converts a ret/action argument to a RetFn (nil when absent or
// false).
func stmRetFn(v any) RetFn {
	switch f := v.(type) {
	case RetFn:
		return f
	case func(any) any:
		if f != nil {
			return RetFn(f)
		}
	}
	return nil
}

// stmApplyContinue binds the continue statements in a loop body to a
// loop-local flag, wrapping the body in a repeat block that continue can
// break out of
func stmApplyContinue(body *ast.Node) *ast.Node {
	continues := findContinues(body, nil)
	if len(continues) == 0 {
		return body
	}

	continueName := NewNameProxy("continue")

	// attach the name in place, the continue transformer expands the node
	// when the compiler reaches it so cascading transforms still see a
	// continue
	for _, n := range continues {
		n.Set(2, continueName)
	}

	last, _, _ := lastStm(body)
	lastType := ntype(last)

	// a trailing continue expands to a break, which must also be enclosed
	repeatBody := body
	if terminating[lastType] || lastType == "continue" {
		repeatBody = node(node("do", body))
	}

	repeatBody.Append(node("assign", node(continueName), node("true")))

	return node(
		node("assign", node(continueName), node("false")),
		node("repeat", "true", repeatBody),
		node("if", node("not", continueName), node(
			node("break"),
		)),
	)
}

// stmExtractDeclarations mutates body searching for assigns. A nil body
// means scope.CurrentStms, start < 0 means scope.CurrentStmI + 1.
func stmExtractDeclarations(scope *Block, body *ast.Node, start int, out *ast.Node) *ast.Node {
	if body == nil {
		body = scope.CurrentStms
	}
	if start < 0 {
		start = scope.CurrentStmI + 1
	}
	if out == nil {
		out = node()
	}
	n := body.Len()
	for i := start; i <= n; i++ {
		stm := body.At(i)
		if stm == nil {
			continue
		}
		stm = scope.TransformStatement(stm)
		body.Set(i, stm)
		switch ntype(stm) {
		case "assign", "declare", "declare_constants":
			names, _ := stm.(*ast.Node).At(2).(*ast.Node)
			for _, name := range names.Items {
				if name == nil {
					break
				}
				if ntype(name) == "ref" {
					out.Append(name)
				} else if _, ok := name.(string); ok {
					// TODO: don't use string literal as ref
					out.Append(name)
				}
			}
		case "group":
			stmExtractDeclarations(scope, stm.(*ast.Node).At(2).(*ast.Node), 1, out)
		}
	}
	return out
}

// stmExtractDeclareNames: the names a declare statement must list for an
// assignment's names, destructuring literals are replaced by the names they
// extract so the declaration works when the assignment happens in a deeper
// scope
func stmExtractDeclareNames(names *ast.Node) *ast.Node {
	out := node()
	for _, name := range names.Items {
		if name == nil {
			break
		}
		if ntype(name) == "table" {
			for _, tuple := range extractAssignNames(name.(*ast.Node)) {
				out.Append(tuple.Target)
			}
		} else {
			out.Append(name)
		}
	}
	return out
}

func stmExpandElseifAssign(ifstm *ast.Node) *ast.Node {
	for i := 4; i <= ifstm.Len(); i++ {
		c, _ := ifstm.At(i).(*ast.Node)
		if ntype(c) == "elseif" && ntype(c.At(2)) == "assign" {
			split := ifstm.Slice(1, i-1)
			inner := node("if", c.At(2), c.At(3))
			inner.Append(ifstm.Rest(i + 1)...)
			split.Append(node("else", node(inner)))
			return split
		}
	}
	return ifstm
}

// stmSpliceNode builds {items..., unpack(src, from)}.
func stmSpliceNode(src *ast.Node, from int, items ...any) *ast.Node {
	n := node(items...)
	n.Append(src.Rest(from)...)
	return n
}

func stmTransform(scope *Block, tuple *ast.Node, extra ...any) any {
	return stmCallRet(tuple.At(3), tuple.At(2))
}

func stmRootStms(scope *Block, body *ast.Node, extra ...any) any {
	return rootStms(scope, body)
}

func stmReturn(scope *Block, n *ast.Node, extra ...any) any {
	retVal := n.At(2)
	retValType := ntype(retVal)

	if retValType == "explist" && retVal.(*ast.Node).Len() == 2 {
		retVal = retVal.(*ast.Node).At(2)
		retValType = ntype(retVal)
	}

	if cascading[retValType] {
		return implicitlyReturn(scope)(retVal)
	}

	// flatten things that create block exp
	if retValType == "chain" || retValType == "comprehension" || retValType == "tblcomprehension" {
		retVal = scope.TransformValueOnce(retVal)
		if ntype(retVal) == "block_exp" {
			return buildGroup(transformLastStm(retVal.(*ast.Node).At(2).(*ast.Node), func(stm any) any {
				return node("return", stm)
			}))
		}
	}

	n.Set(2, retVal)
	return n
}

func stmDeclareGlob(scope *Block, n *ast.Node, extra ...any) any {
	names := stmExtractDeclarations(scope, nil, -1, nil)

	if s, _ := n.At(2).(string); s == "^" {
		filtered := node()
		for _, name := range names.Items {
			var strName string
			if ntype(name) == "ref" {
				strName, _ = name.(*ast.Node).At(2).(string)
			} else {
				strName, _ = name.(string)
			}
			if !matchUpper(strName) {
				continue
			}
			filtered.Append(name)
		}
		names = filtered
	}

	return node("declare", names)
}

func stmAssign(scope *Block, n *ast.Node, extra ...any) any {
	names, values := n.At(2).(*ast.Node), n.At(3).(*ast.Node)

	numValues := values.Len()
	numNames := names.Len()

	// special code simplifications for single assigns
	if numNames == 1 && numValues == 1 {
		firstValue := values.At(1)
		firstName := names.At(1)
		firstType := ntype(firstValue)

		// reduce colon stub chain to block exp
		if firstType == "chain" {
			firstValue = scope.TransformValueOnce(firstValue)
			firstType = ntype(firstValue)
		}

		switch firstType {
		case "block_exp":
			blockBody := firstValue.(*ast.Node).At(2).(*ast.Node)
			idx := blockBody.Len()
			last := blockBody.At(idx)
			assign := buildAssignOne(firstName, last)
			// position of the value being assigned, the name is from the
			// line of the enclosing assign
			if isTable(last) {
				setPos(assign, posOf(last))
			}
			blockBody.Set(idx, assign)

			return buildGroup(node(
				node("declare", stmExtractDeclareNames(node(firstName))),
				node("do", blockBody),
			))
		case "comprehension", "tblcomprehension", "foreach", "for", "while":
			return buildAssignOne(firstName, scope.TransformValueOnce(firstValue))
		default:
			values.Set(1, firstValue)
		}
	}

	// bubble cascading assigns
	if numValues == 1 {
		value := values.At(1)
		t := ntype(value)

		if t == "decorated" {
			value = scope.TransformStatement(value)
			t = ntype(value)
		}

		if cascading[t] {
			ret := RetFn(func(stm any) any {
				if isValue(stm) {
					assign := node("assign", names, node(stm))
					// position of the value being assigned, the name is
					// from the line of the enclosing assign
					if isTable(stm) {
						setPos(assign, posOf(stm))
					}
					return assign
				}
				return stm
			})

			declare := node("declare", stmExtractDeclareNames(names))
			// the assigns bubbled into the value's branches handle their own
			// destructuring when they are transformed
			return buildGroup(node(
				declare,
				scope.TransformStatement(value, ret, n),
			))
		}
	}

	if hasDestructure(names) {
		return splitAssign(scope, n)
	}

	return n
}

// stmContinue: apply_continue binds the loop's flag to the node, a continue
// without one is outside of any loop
func stmContinue(scope *Block, n *ast.Node, extra ...any) any {
	continueName := n.At(2)
	if !truthy(continueName) {
		userError("continue must be inside of a loop", n.Pos)
	}
	return buildGroup(node(
		buildAssignOne(continueName, "true"),
		node("break"),
	))
}

func stmExport(scope *Block, n *ast.Node, extra ...any) any {
	// assign values if they are included
	if n.Len() > 2 {
		if s, _ := n.At(2).(string); s == "class" {
			cls := n.At(3).(*ast.Node)
			return buildGroup(node(
				node("export", node(cls.At(2))),
				cls,
			))
		}
		// pull out values and assign them after the export
		return buildGroup(node(
			node("export", n.At(2)),
			buildAssign(asNode(n.At(2)), asNode(n.At(3))),
		))
	}
	return nil
}

func stmUpdate(scope *Block, n *ast.Node, extra ...any) any {
	name, op, exp := n.At(2), n.At(3), n.At(4)
	opStr, _ := op.(string)
	if len(opStr) < 2 || !strings.HasSuffix(opStr, "=") {
		panic("Unknown op: " + opStr)
	}
	opFinal := opStr[:len(opStr)-1]

	var lifted [][2]any
	isLifted := false

	if ntype(name) == "chain" {
		isLifted = true
		chain := name.(*ast.Node)
		var newChain []any
		for i := 3; i <= chain.Len(); i++ {
			part := chain.At(i)
			if ntype(part) == "index" {
				proxy := NewNameProxy("update")
				lifted = append(lifted, [2]any{proxy, part.(*ast.Node).At(2)})
				newChain = append(newChain, node("index", proxy))
			} else {
				newChain = append(newChain, part)
			}
		}

		if len(lifted) > 0 {
			nn := node(chain.At(1), chain.At(2))
			nn.Append(newChain...)
			name = nn
		}
	}

	if !valueIsSingular(exp) {
		exp = node("parens", exp)
	}
	var out any = buildAssignOne(name, node("exp", name, opFinal, exp))

	if isLifted && len(lifted) > 0 {
		names, values := node(), node()
		for _, l := range lifted {
			names.Append(l[0])
		}
		for _, l := range lifted {
			values.Append(l[1])
		}
		out = buildGroup(node(
			node("assign", names, values),
			out,
		))
	}

	return out
}

func stmImport(scope *Block, n *ast.Node, extra ...any) any {
	names, source := n.At(2).(*ast.Node), n.At(3)

	destNames := node()
	tableValues := node()
	for _, name := range names.Items {
		if name == nil {
			break
		}
		destName := name
		if ntype(name) == "colon" {
			destName = name.(*ast.Node).At(2)
		}
		destNames.Append(destName)
		tableValues.Append(node(node("key_literal", name), destName))
	}

	dest := node("table", tableValues)
	return buildGroup(node(
		ast.NewPos(n.Pos, "assign", node(dest), node(source)),
		ast.NewPos(n.Pos, "declare_constants", destNames),
	))
}

func stmComprehension(scope *Block, n *ast.Node, extra ...any) any {
	exp, clauses := n.At(2), n.At(3).(*ast.Node)

	var body *ast.Node
	if action := extraArg(extra, 0); stmIsFn(action) {
		body = asNode(stmCallRet(action, exp))
	} else {
		body = node(exp)
	}
	return constructComprehension(body, clauses)
}

func stmDo(scope *Block, n *ast.Node, extra ...any) any {
	if ret := stmRetFn(extraArg(extra, 0)); ret != nil {
		n.Set(2, transformLastStm(n.At(2).(*ast.Node), ret))
	}
	return n
}

func stmDecorated(scope *Block, n *ast.Node, extra ...any) any {
	stm, dec := n.At(2), n.At(3).(*ast.Node)

	var wrapped *ast.Node
	switch dec.Kind() {
	case "if":
		cond, fail := dec.At(2), dec.At(3)
		wrapped = node("if", cond, node(stm))
		if truthy(fail) {
			wrapped.Append(node("else", node(fail)))
		}
	case "unless":
		wrapped = node("unless", dec.At(2), node(stm))
	case "comprehension":
		wrapped = node("comprehension", stm, dec.At(2))
	default:
		panic("Unknown decorator " + dec.Kind())
	}

	if ntype(stm) == "assign" {
		names := node()
		for _, name := range stmExtractDeclareNames(stm.(*ast.Node).At(2).(*ast.Node)).Items {
			if ntype(name) == "ref" {
				names.Append(name)
			}
		}
		wrapped = buildGroup(node(
			buildDeclare(names),
			wrapped,
		))
	}

	return wrapped
}

func stmUnless(scope *Block, n *ast.Node, extra ...any) any {
	clause := n.At(2)

	if ntype(clause) == "assign" {
		c := clause.(*ast.Node)
		if hasDestructure(c.At(2).(*ast.Node)) {
			panic("destructure not allowed in unless assignment")
		}

		return buildDo(node(
			clause,
			stmSpliceNode(n, 3, "if", node("not", c.At(2).(*ast.Node).At(1))),
		))
	}

	return stmSpliceNode(n, 3, "if", node("not", node("parens", clause)))
}

func stmIf(scope *Block, n *ast.Node, extra ...any) any {
	ret := stmRetFn(extraArg(extra, 0))

	// expand assign in cond
	if ntype(n.At(2)) == "assign" {
		assign := n.At(2).(*ast.Node)
		assignNames := assign.At(2).(*ast.Node)
		if hasDestructure(assignNames) {
			name := NewNameProxy("des")

			des := buildDestructureAssign(scope, assignNames.At(1).(*ast.Node), name, false)
			body := node(
				des,
				buildGroup(asNode(n.At(3))),
			)

			first := buildAssignOne(name, assign.At(3).(*ast.Node).At(1))
			return buildDo(node(
				first,
				stmSpliceNode(n, 4, "if", name, body),
			))
		}
		name := assignNames.At(1)
		return buildDo(node(
			assign,
			stmSpliceNode(n, 3, "if", name),
		))
	}

	n = stmExpandElseifAssign(n)

	// apply cascading return decorator
	if ret != nil {
		// mutate all the bodies
		n.Set(3, transformLastStm(n.At(3).(*ast.Node), ret))
		for i := 4; i <= n.Len(); i++ {
			c := n.At(i).(*ast.Node)
			bodyIdx := c.Len()
			c.Set(bodyIdx, transformLastStm(c.At(bodyIdx).(*ast.Node), ret))
		}
	}

	return n
}

func stmWith(scope *Block, n *ast.Node, extra ...any) any {
	ret := stmRetFn(extraArg(extra, 0))
	exp, block := n.At(2), n.At(3).(*ast.Node)

	copyScope := true
	var scopeName, namedAssign any

	if last, _, _ := lastStm(block); truthy(last) {
		if terminating[ntype(last)] {
			ret = nil
		}
	}

	if ntype(exp) == "assign" {
		e := exp.(*ast.Node)
		names, values := e.At(2).(*ast.Node), e.At(3).(*ast.Node)
		firstName := names.At(1)

		if ntype(firstName) == "ref" {
			scopeName = firstName
			namedAssign = exp
			exp = values.At(1)
			copyScope = false
		} else {
			scopeName = NewNameProxy("with")
			exp = values.At(1)
			values.Set(1, scopeName)
			namedAssign = node("assign", names, values)
		}
	} else if scope.IsLocal(exp) {
		scopeName = exp
		copyScope = false
	}

	if !truthy(scopeName) {
		scopeName = NewNameProxy("with")
	}

	var first any = noop()
	if copyScope {
		first = buildAssignOne(scopeName, exp)
	}
	if namedAssign == nil {
		namedAssign = noop()
	}
	sn := scopeName
	body := node(
		first,
		namedAssign,
		NewRun(func(s *Block) { s.Set("scope_var", sn) }),
	)
	body.Append(block.Items...)
	out := buildDo(body)

	if ret != nil {
		if v := ret(scopeName); v != nil {
			out.At(2).(*ast.Node).Append(v)
		}
	}

	return out
}

func stmForeach(scope *Block, n *ast.Node, extra ...any) any {
	iter := n.At(3).(*ast.Node)
	source := iter.At(1)

	var destructures []any
	newNames := node()
	for _, name := range n.At(2).(*ast.Node).Items {
		if name == nil {
			break
		}
		if ntype(name) == "table" {
			proxy := NewNameProxy("des")
			destructures = append(destructures, buildDestructureAssign(scope, name.(*ast.Node), proxy, false))
			newNames.Append(proxy)
		} else {
			newNames.Append(name)
		}
	}
	n.Set(2, newNames)

	if len(destructures) > 0 {
		destructures = append(destructures, buildGroup(asNode(n.At(4))))
		n.Set(4, node(destructures...))
	}

	if ntype(source) == "unpack" {
		list := source.(*ast.Node).At(2)

		indexName := NewNameProxy("index")

		var listName any
		if scope.IsLocal(list) {
			listName = list
		} else {
			listName = NewNameProxy("list")
		}

		var sliceVar any
		var bounds *ast.Node
		if isSlice(list) {
			l := list.(*ast.Node)
			slice := l.Last().(*ast.Node)
			l.Remove()
			slice.Remove(1)

			if scope.IsLocal(list) {
				listName = list
			}

			if s2 := slice.At(2); truthy(s2) && !same(s2, "") {
				maxTmpName := NewNameProxy("max")
				sliceVar = buildAssignOne(maxTmpName, s2)
				slice.Set(2, node("exp", maxTmpName, "<", 0,
					"and", node("length", listName), "+", maxTmpName,
					"or", maxTmpName))
			} else {
				slice.Set(2, node("length", listName))
			}

			bounds = slice
		} else {
			bounds = node(1, node("length", listName))
		}

		names := node()
		for _, nm := range n.At(2).(*ast.Node).Items {
			if nm == nil {
				break
			}
			if isNameProxy(nm) {
				names.Append(nm)
			} else if s, ok := nm.(string); ok {
				names.Append(NewLocalName(s))
			} else {
				names.Append(nm)
			}
		}

		var first any = noop()
		if !same(listName, list) {
			first = buildAssignOne(listName, list)
		}
		if sliceVar == nil {
			sliceVar = noop()
		}
		return buildGroup(node(
			first,
			sliceVar,
			buildFor(indexName, bounds, node(
				node("assign", names, node(proxyIndex(listName, indexName))),
				buildGroup(asNode(n.At(4))),
			)),
		))
	}

	n.Set(4, stmApplyContinue(n.At(4).(*ast.Node)))
	return nil
}

func stmWhile(scope *Block, n *ast.Node, extra ...any) any {
	n.Set(3, stmApplyContinue(n.At(3).(*ast.Node)))
	return nil
}

func stmFor(scope *Block, n *ast.Node, extra ...any) any {
	n.Set(4, stmApplyContinue(n.At(4).(*ast.Node)))
	return nil
}

func stmSwitch(scope *Block, n *ast.Node, extra ...any) any {
	ret := stmRetFn(extraArg(extra, 0))
	exp, conds := n.At(2), n.At(3).(*ast.Node)
	expName := NewNameProxy("exp")

	// convert switch conds into if statement conds
	convertCond := func(cond *ast.Node) *ast.Node {
		t, caseExps, body := cond.Kind(), cond.At(2), cond.At(3)
		out := node()
		if t == "case" {
			out.Append("elseif")
		} else {
			out.Append("else")
		}
		if t != "else" {
			condExp := node()
			for i, c := range caseExps.(*ast.Node).Items {
				if c == nil {
					break
				}
				if i == 0 {
					condExp.Append("exp")
				} else {
					condExp.Append("or")
				}
				if !valueIsSingular(c) {
					c = node("parens", c)
				}
				condExp.Append(node("exp", c, "==", expName))
			}
			out.Append(condExp)
		} else {
			body = caseExps
		}

		if ret != nil {
			body = transformLastStm(body.(*ast.Node), ret)
		}

		if body != nil {
			out.Append(body)
		}
		return out
	}

	first := true
	ifStm := node("if")
	for _, cond := range conds.Items {
		if cond == nil {
			break
		}
		ifCond := convertCond(cond.(*ast.Node))
		if first {
			first = false
			if v := ifCond.At(2); v != nil {
				ifStm.Append(v)
			}
			if v := ifCond.At(3); v != nil {
				ifStm.Append(v)
			}
		} else {
			ifStm.Append(ifCond)
		}
	}

	return buildGroup(node(
		buildAssignOne(expName, exp),
		ifStm,
	))
}

func init() {
	for name, fn := range map[string]TransformFn{
		"transform":     stmTransform,
		"root_stms":     stmRootStms,
		"return":        stmReturn,
		"declare_glob":  stmDeclareGlob,
		"assign":        stmAssign,
		"continue":      stmContinue,
		"export":        stmExport,
		"update":        stmUpdate,
		"import":        stmImport,
		"comprehension": stmComprehension,
		"do":            stmDo,
		"decorated":     stmDecorated,
		"unless":        stmUnless,
		"if":            stmIf,
		"with":          stmWith,
		"foreach":       stmForeach,
		"while":         stmWhile,
		"for":           stmFor,
		"switch":        stmSwitch,
	} {
		statementTransformers[name] = fn
	}
}
