package parse

// The Lua snippets embedded in grammar.moon, referenced by name from the
// grammar description (internal/grammar) and called by the generated parser.

import "github.com/tarantool-contrib/go-moonscript/moonscript/ast"

func arg(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return nil
}

func posFn(args []any) any {
	p := args[0].(int)
	value := arg(args, 1)
	if n, ok := value.(*ast.Node); ok {
		n.Pos = p
	}
	return value
}

func statementFn(args []any) any {
	stm, dec := arg(args, 0), arg(args, 1)
	if dec != nil {
		return ast.New("decorated", stm, dec)
	}
	return stm
}

func assignTransformFn(args []any) any {
	return formatAssign(args[0].(*ast.Node), arg(args, 1))
}

func singleAssignFn(args []any) any {
	return formatSingleAssign(args[0], arg(args, 1))
}

func expFn(args []any) any {
	if len(args) == 1 {
		return args[0]
	}
	return ast.New(append([]any{"exp"}, args...)...)
}

func joinChainFn(args []any) any {
	return joinChain(args[0], args[1].(*ast.Node))
}

func luaStringFn(args []any) any {
	eqStart, eqEnd := args[0].(int), args[1].(int)
	content := arg(args, 2)
	eq := ""
	for i := 0; i < eqEnd-eqStart; i++ {
		eq += "="
	}
	return ast.New("string", "["+eq+"[", content)
}

func keyValueShorthandFn(args []any) any {
	name, p := args[0].(string), args[1].(int)
	return ast.New(
		ast.New("key_literal", name),
		ast.NewPos(p, "ref", name),
	)
}

func assignableCmt(subject string, pos int, args []any) (bool, int, []any) {
	node := args[0].(*ast.Node)
	if last, ok := node.Last().(*ast.Node); ok {
		switch last.Kind() {
		case "dot", "index", "slice":
			return true, pos, []any{node}
		}
	}
	return false, 0, nil
}
