package compiler

// Port of moonscript/transform/class.moon.

import (
	"github.com/tarantool-contrib/go-moonscript/ast"
)

const clsConstructorName = "new"

// clsTransformSuper is transform_super: it rewrites a `super` reference
// (chain == nil) or a chain starting with `super` into a lookup on the
// parent class.
func clsTransformSuper(clsName *NameProxy, onBase bool, block *Block, chain *ast.Node) any {
	relativeParent := node("chain", clsName, node("dot", "__parent"))

	if chain == nil {
		return relativeParent
	}

	chainTail := chain.Slice(3)
	head := chainTail.At(1)

	if head == nil {
		return relativeParent
	}
	headN := head.(*ast.Node)

	newChain := relativeParent

	switch headN.At(1) {
	// calling super, inject calling name and self into chain
	case "call":
		if onBase {
			newChain.Append(node("dot", "__base"))
		}

		callingName := block.Get("current_method")
		if !truthy(callingName) {
			panic("missing calling name")
		}
		args := node("self")
		if a, ok := headN.At(2).(*ast.Node); ok {
			args.Append(a.Items...)
		}
		chainTail.Set(1, node("call", args))

		if ntype(callingName) == "key_literal" {
			newChain.Append(node("dot", callingName.(*ast.Node).At(2)))
		} else {
			newChain.Append(node("index", callingName))
		}

	// colon call on super, replace class with self as first arg
	case "colon":
		call := chainTail.At(2)
		// calling chain tail
		if cn, ok := call.(*ast.Node); ok && cn.At(1) == "call" {
			chainTail.Set(1, node("dot", headN.At(2)))
			args := node("self")
			if a, ok := cn.At(2).(*ast.Node); ok {
				args.Append(a.Items...)
			}
			chainTail.Set(2, node("call", args))
		}
	}

	for _, item := range chainTail.Items {
		if item == nil {
			break
		}
		newChain.Append(item)
	}
	return newChain
}

// clsSuperScope is super_scope: wraps value in a scoped node that sets the
// current method and super handler while value is compiled.
func clsSuperScope(value any, t SuperFn, key any) *ast.Node {
	var prevMethod any

	return node(
		"scoped",
		NewRun(func(b *Block) {
			prevMethod = b.Get("current_method")
			b.Set("current_method", key)
			b.Set("super", t)
		}),
		value,
		NewRun(func(b *Block) {
			b.Set("current_method", prevMethod)
		}),
	)
}

func init() {
	statementTransformers["class"] = clsTransform
}

// clsTransform is the class statement transformer:
// (node, ret, parent_assign) => ...
func clsTransform(scope *Block, n *ast.Node, extra ...any) any {
	ret := retArg(extra, 0)
	parentAssign, _ := extraArg(extra, 1).(*ast.Node)

	name, parentVal, body := n.At(2), n.At(3), n.At(4)
	if !truthy(name) {
		name = nil // anonymous class have `false` set for the name
	}
	if s, ok := parentVal.(string); ok && s == "" {
		parentVal = nil
	}
	if !truthy(parentVal) {
		parentVal = nil
	}

	parentClsName := NewNameProxy("parent")
	baseName := NewNameProxy("base")
	selfName := NewNameProxy("self")
	clsName := NewNameProxy("class")

	// super call on instance
	clsInstanceSuper := SuperFn(func(b *Block, chain *ast.Node) any {
		return clsTransformSuper(clsName, true, b, chain)
	})

	// super call on parent class
	clsSuper := SuperFn(func(b *Block, chain *ast.Node) any {
		return clsTransformSuper(clsName, false, b, chain)
	})

	// split apart properties and statements
	statements := node()
	properties := node()

	// local declarations are hoisted to the top of the class scope so
	// methods can close over the names
	hoistedLocals := node()

	if bn, ok := body.(*ast.Node); ok {
		for _, it := range bn.Items {
			if it == nil {
				break
			}
			item := it.(*ast.Node)
			switch item.At(1) {
			case "stm":
				stm := item.At(2)
				if ntype(stm) == "declare_with_shadows" {
					hoistedLocals.Append(stm)
				} else {
					statements.Append(stm)
				}
			case "props":
				for _, tp := range item.Rest(2) {
					if tp == nil {
						break
					}
					tuple := tp.(*ast.Node)
					if ntype(tuple.At(1)) == "self" {
						k, v := tuple.At(1).(*ast.Node), tuple.At(2)
						v = clsSuperScope(v, clsSuper, node("key_literal", k.At(2)))
						statements.Append(buildAssignOne(k, v))
					} else {
						properties.Append(tuple)
					}
				}
			}
		}
	}

	// find constructor
	var constructor any
	newProps := node()
	for _, tp := range properties.Items {
		tuple := tp.(*ast.Node)
		key := tuple.At(1)
		if kn, ok := key.(*ast.Node); ok && kn.At(1) == "key_literal" && kn.At(2) == clsConstructorName {
			constructor = tuple.At(2)
			continue
		}
		val := tuple.At(2)
		newProps.Append(node(key, clsSuperScope(val, clsInstanceSuper, key)))
	}
	properties = newProps

	if !truthy(constructor) {
		if parentVal != nil {
			constructor = buildFndef(
				node(node("...")),
				nil,
				"fat",
				node(buildChain("super", node("call", node("...")))),
			)
		} else {
			constructor = buildFndef(nil, nil, "", nil)
		}
	}

	realName := name
	if !truthy(realName) {
		realName = nil
		if parentAssign != nil {
			realName = parentAssign.At(2).(*ast.Node).At(1)
		}
	}
	switch ntype(realName) {
	case "chain":
		last := realName.(*ast.Node).Last()
		switch ntype(last) {
		case "dot":
			realName = node("string", `"`, last.(*ast.Node).At(2))
		case "index":
			realName = last.(*ast.Node).At(2)
		default:
			realName = "nil"
		}
	case "nil":
		realName = "nil"
	default:
		// TODO: don't use string literal as ref
		var flattened any
		if s, ok := realName.(string); ok {
			flattened = s
		} else if rn, ok := realName.(*ast.Node); ok && rn.At(1) == "ref" {
			flattened = rn.At(2)
		} else {
			panic("don't know how to extract name from " + clsLuaType(realName))
		}
		realName = node("string", `"`, flattened)
	}

	var parentTuple *ast.Node
	if parentVal != nil {
		parentTuple = node("__parent", parentClsName)
	}
	cls := buildTable(
		node("__init", clsSuperScope(constructor, clsSuper, node("key_literal", "__init"))),
		node("__base", baseName),
		node("__name", realName), // "quote the string"
		parentTuple,
	)

	// looking up a name in the class object
	var classIndex any
	if parentVal != nil {
		classLookup := buildIf(
			node("exp", node("ref", "val"), "==", "nil"),
			node(
				buildAssignOne(NewLocalName("parent"), buildChain("rawget",
					node("call", node(
						node("ref", "cls"),
						node("string", `"`, "__parent"),
					)),
				)),
				buildIf(NewLocalName("parent"), node(
					buildChain(NewLocalName("parent"), node("index", "name")),
				)),
			),
		)
		classLookup.Append(node("else", node("val")))

		classIndex = buildFndef(
			node(node("cls"), node("name")),
			nil, "",
			node(
				buildAssignOne(NewLocalName("val"), buildChain(
					"rawget", node("call", node(baseName, node("ref", "name"))),
				)),
				classLookup,
			),
		)
	} else {
		classIndex = baseName
	}

	clsMt := buildTable(
		node("__index", classIndex),
		node("__call", buildFndef(
			node(node("cls"), node("...")),
			nil, "",
			node(
				buildAssignOne(selfName, buildChain(
					"setmetatable",
					node("call", node("{}", baseName)),
				)),
				buildChain("cls.__init", node("call", node(selfName, "..."))),
				selfName,
			),
		)),
	)

	cls = buildChain("setmetatable", node("call", node(cls, clsMt)))

	var parentAssignStm any = noop()
	var parentMeta any = noop()
	var inherited any = noop()
	if parentVal != nil {
		parentAssignStm = buildAssignOne(parentClsName, parentVal)
		parentMeta = buildChain("setmetatable", node("call", node(
			baseName,
			buildChain(parentClsName, node("dot", "__base")),
		)))
		// run the inherited callback
		inherited = buildIf(
			node("exp", parentClsName.Chain("__inherited")),
			node(parentClsName.Chain("__inherited", node("call", node(parentClsName, clsName)))),
		)
	}

	var hoisted *ast.Node
	if hoistedLocals.Len() > 0 {
		hoisted = hoistedLocals
	}

	var stmsGroup *ast.Node
	if statements.Len() > 0 {
		stmsGroup = node(
			buildAssignOne(NewLocalName("self"), clsName),
			buildGroup(statements),
		)
	}

	var nameAssign *ast.Node
	if name != nil {
		nameAssign = node(buildAssignOne(name, clsName))
	}

	outBody := node(
		NewRun(func(b *Block) {
			// make sure we don't assign the class to a local inside the do
			if name != nil {
				clsPutName(b, name)
			}
		}),

		node("declare", node(clsName)),

		// the parent expression is evaluated before the local shadows are
		// installed so it can reference a name a class body local overrides
		parentAssignStm,

		buildGroup(hoisted),

		node("declare_glob", "*"),

		buildAssignOne(baseName, node("table", properties)),
		buildAssignOne(baseName.Chain("__index"), baseName),

		parentMeta,

		buildAssignOne(clsName, cls),
		buildAssignOne(baseName.Chain("__class"), clsName),

		buildGroup(stmsGroup),

		inherited,

		buildGroup(nameAssign),
	)

	if ret != nil {
		if r := ret(clsName); r != nil {
			outBody.Append(r)
		}
	}

	var declareGroup *ast.Node
	if ntype(name) == "value" {
		declareGroup = node(buildDeclare(node(name)))
	}

	return buildGroup(node(
		buildGroup(declareGroup),
		buildDo(outBody),
	))
}

// clsPutName is `@put_name name`: the reference also accepts tables (eg. a
// chain name), storing them under a table key that no lookup by string can
// ever hit, so only strings and proxies are recorded.
func clsPutName(b *Block, name any) {
	switch name.(type) {
	case string, *NameProxy:
		b.PutName(name)
	}
}

// clsLuaType is Lua's type() for tree values.
func clsLuaType(v any) string {
	switch v.(type) {
	case nil:
		return "nil"
	case string:
		return "string"
	case bool:
		return "boolean"
	case int:
		return "number"
	}
	if isTable(v) {
		return "table"
	}
	return "userdata"
}
