package compiler

// Port of moonscript/compile/value.moon.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
)

// ValueCompiler compiles a value. n is the (transformed) value: an
// *ast.Node, a table-backed object (*NameProxy, *LocalName) or a raw value.
// Returns string, *Line or *Block.
type ValueCompiler func(b *Block, n any, extra ...any) any

var valueCompilers map[string]ValueCompiler

// SuperFn is the value stored under the "super" state key by the class
// transformer: given the block and the chain being compiled (nil for a bare
// `super` ref) it returns the replacement node.
type SuperFn func(b *Block, chain *ast.Node) any

const tableDelim = ","

// lua binary operator precedence, from loosest to tightest binding
var binaryOpPrec = func() map[string]int {
	out := map[string]int{}
	for prec, ops := range [][]string{
		{"or"},
		{"and"},
		{"<", ">", "<=", ">=", "~=", "!=", "=="},
		{"|"},
		{"&"},
		{"<<", ">>"},
		{".."},
		{"+", "-"},
		{"*", "/", "//", "%"},
		{"^"},
	} {
		for _, op := range ops {
			out[op] = prec + 1
		}
	}
	return out
}()

var rightAssocOp = set("..", "^")

func opPrec(v any) int {
	if s, ok := v.(string); ok {
		return binaryOpPrec[s]
	}
	return 0
}

// expPrecedence is the loosest binding operator in a flat exp node, 0 if
// there are none
func expPrecedence(n *ast.Node) int {
	minPrec := 0
	for i := 3; i <= n.Len(); i += 2 {
		if prec := opPrec(n.At(i)); prec != 0 {
			if minPrec == 0 || prec < minPrec {
				minPrec = prec
			}
		}
	}
	return minPrec
}

// luaToString is tostring for raw tree values.
func luaToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return "nil"
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case fmt.Stringer:
		return t.String()
	}
	return fmt.Sprint(v)
}

func init() {
	valueCompilers = map[string]ValueCompiler{
		"scoped": func(b *Block, v any, _ ...any) any {
			n := v.(*ast.Node)
			before, value, after := n.At(2), n.At(3), n.At(4)
			if r, ok := before.(*Run); ok {
				r.Call(b)
			}
			out := b.Value(value)
			if r, ok := after.(*Run); ok {
				r.Call(b)
			}
			return out
		},

		// list of values separated by binary operators
		"exp": func(b *Block, v any, _ ...any) any {
			n := v.(*ast.Node)
			// exp nodes nested by transformations (eg. string interpolation)
			// must keep their grouping if an adjacent operator binds tighter
			// than one of their own operators
			needsParens := func(value any, i int) bool {
				vn, ok := value.(*ast.Node)
				if !ok || vn.Kind() != "exp" {
					return false
				}
				inner := expPrecedence(vn)
				if inner == 0 {
					return false
				}
				if i > 2 {
					if left := opPrec(n.At(i - 1)); left != 0 {
						if left > inner {
							return true
						}
						if left == inner && !rightAssocOp[luaToString(n.At(i-1))] {
							return true
						}
					}
				}
				if i < n.Len() {
					if right := opPrec(n.At(i + 1)); right != 0 {
						if right > inner {
							return true
						}
						// equal precedence on the right regroups under a right
						// associative operator: an exp holding a .. b rendered
						// flat as a .. b .. c evaluates as a .. (b .. c),
						// observable through __concat
						if right == inner && rightAssocOp[luaToString(n.At(i+1))] {
							return true
						}
					}
				}
				return false
			}

			comp := func(i int, value any) any {
				if s, ok := value.(string); ok && i%2 == 1 && s == "!=" {
					value = "~="
				}
				// transform now so nested exps (eg. from string
				// interpolation) are visible to the parenthesization check
				if isTable(value) {
					value = b.TransformValue(value)
				}
				if needsParens(value, i) {
					return b.Line("(", b.Value(value), ")")
				}
				return b.Value(value)
			}

			var parts []any
			for i, value := range n.Items {
				if value == nil {
					break
				}
				if i+1 > 1 {
					parts = append(parts, comp(i+1, value))
				}
			}
			l := b.Line()
			l.AppendList(parts, " ")
			return l
		},

		// list of expressions separated by commas
		"explist": func(b *Block, v any, _ ...any) any {
			n := v.(*ast.Node)
			l := b.Line()
			l.AppendList(b.valueList(n.Rest(2)), ", ")
			return l
		},

		"parens": func(b *Block, v any, _ ...any) any {
			return b.Line("(", b.Value(v.(*ast.Node).At(2)), ")")
		},

		"string": func(b *Block, v any, _ ...any) any {
			n := v.(*ast.Node)
			delim := n.At(2).(string)
			inner := luaToString(n.At(3))
			if n.At(3) == nil {
				inner = ""
			}
			endDelim := strings.ReplaceAll(delim, "[", "]")
			// a string node that's already been transformed (eg. an
			// interpolated class method key reused by super) still holds
			// its parts; the reference crashes on them
			if isTable(n.At(3)) {
				if delim == "'" || delim == `"` {
					panic("attempt to call method 'gsub' (a nil value)")
				}
				panic("attempt to concatenate local 'inner' (a table value)")
			}
			if delim == "'" || delim == `"` {
				inner = strings.NewReplacer("\r", `\r`, "\n", `\n`).Replace(inner)
			}
			return delim + inner + endDelim
		},

		"chain": func(b *Block, v any, _ ...any) any {
			n := v.(*ast.Node)
			callee := n.At(2)
			calleeType := ntype(callee)
			itemOffset := 3

			if calleeType == "dot" || calleeType == "colon" || calleeType == "index" {
				callee = b.Get("scope_var")
				if callee == nil {
					userError("Short-dot syntax must be called within a with block", n.Pos)
				}
				itemOffset = 2
			}

			// TODO: don't use string literals as ref
			if calleeType == "ref" && callee.(*ast.Node).At(2) == "super" || callee == "super" {
				if sup, ok := b.Get("super").(SuperFn); ok && sup != nil {
					return b.Value(sup(b, n))
				}
			}

			chainItem := func(item *ast.Node) []any {
				t, arg := item.Kind(), item.At(2)
				switch t {
				case "call":
					return []any{"(", b.Values(arg.(*ast.Node)), ")"}
				case "index":
					return []any{"[", b.Value(arg), "]"}
				case "dot":
					return []any{".", luaToString(arg)}
				case "colon":
					return []any{":", luaToString(arg)}
				}
				panic("Unknown chain action: " + t)
			}

			if (calleeType == "self" || calleeType == "self_class") && n.At(3) != nil && ntype(n.At(3)) == "call" {
				callee.(*ast.Node).Set(1, calleeType+"_colon")
			}

			calleeValue := b.Value(callee)

			// expressions and table literals can't be subscripted directly in
			// lua
			switch ntype(callee) {
			case "exp", "table":
				calleeValue = b.Line("(", calleeValue, ")")
			}

			actions := b.Line()
			for i := itemOffset; i <= n.Len(); i++ {
				actions.Append(chainItem(n.At(i).(*ast.Node))...)
			}
			return b.Line(calleeValue, actions)
		},

		"fndef": func(b *Block, v any, _ ...any) any {
			n := v.(*ast.Node)
			args, whitelist, arrow, block := n.At(2).(*ast.Node), n.At(3).(*ast.Node), n.At(4), n.At(5).(*ast.Node)

			var defaultArgs []*ast.Node
			selfArgs := node()

			fnBlock := b.Block(nil, nil)

			var argNames []string
			for _, a := range args.Items {
				arg := a.(*ast.Node)
				name, defaultValue := arg.At(1), arg.At(2)
				var str string
				switch t := name.(type) {
				case string:
					str = t
				case *NameProxy:
					// a destructuring arg holds a proxy. put the resolved
					// name into scope immediately or a second proxy would
					// pick the same free name
					str = t.GetName(fnBlock, false)
				case *LocalName:
					str = t.GetName(fnBlock, false)
				case *ast.Node:
					if t.Kind() == "self" || t.Kind() == "self_class" {
						selfArgs.Append(t)
					}
					str = luaToString(t.At(2))
				}
				if defaultValue != nil {
					defaultArgs = append(defaultArgs, arg)
				}
				argNames = append(argNames, str)
			}

			if arrow == "fat" {
				argNames = append([]string{"self"}, argNames...)
			}

			fnBlock.Header = "function(" + strings.Join(argNames, ", ") + ")"

			if whitelist.Len() > 0 {
				fnBlock.WhitelistNames(whitelist)
			}

			for _, name := range argNames {
				fnBlock.PutName(name)
			}

			for _, def := range defaultArgs {
				name, value := def.At(1), def.At(2)
				switch t := name.(type) {
				case *NameProxy:
					name = t.GetName(fnBlock)
				case *LocalName:
					name = t.GetName(fnBlock)
				case *ast.Node:
					name = t.At(2)
				}
				fnBlock.Stm(node(
					"if", node("exp", node("ref", name), "==", "nil"), node(
						node("assign", node(name), node(value)),
					),
				))
			}

			if selfArgs.Len() > 0 {
				selfArgValues := node()
				for _, a := range selfArgs.Items {
					selfArgValues.Append(a.(*ast.Node).At(2))
				}
				fnBlock.Stm(node("assign", selfArgs, selfArgValues))
			}

			fnBlock.Stms(block)
			return fnBlock
		},

		"table": func(b *Block, v any, _ ...any) any {
			n := v.(*ast.Node)
			items, _ := n.At(2).(*ast.Node)
			blk := b.Block("{", "}")
			formatLine := func(tuple *ast.Node) *Line {
				if tuple.Len() == 2 {
					key, value := tuple.At(1), tuple.At(2)

					// escape keys that are lua keywords
					if ntype(key) == "key_literal" {
						if s, ok := key.(*ast.Node).At(2).(string); ok && luaKeywords[s] {
							key = node("string", `"`, s)
						}
					}

					var assign any
					if ntype(key) == "key_literal" {
						assign = key.(*ast.Node).At(2)
					} else {
						assign = b.Line("[", blk.Value(key), "]")
					}
					return b.Line(assign, " = ", blk.Value(value))
				}
				return b.Line(blk.Value(tuple.At(1)))
			}

			if items != nil {
				count := items.Len()
				for i, t := range items.Items {
					if t == nil {
						break
					}
					line := formatLine(t.(*ast.Node))
					if count != i+1 {
						line.Append(tableDelim)
					}
					blk.Add(line)
				}
			}
			return blk
		},

		"minus": func(b *Block, v any, _ ...any) any {
			return b.Line("-", b.Value(v.(*ast.Node).At(2)))
		},

		"temp_name": func(b *Block, v any, extra ...any) any {
			dontPut := []bool{}
			if len(extra) > 0 {
				if d, ok := extra[0].(bool); ok {
					dontPut = append(dontPut, d)
				}
			}
			return v.(nameHolder).GetName(b, dontPut...)
		},

		"number": func(b *Block, v any, _ ...any) any {
			return luaToString(v.(*ast.Node).At(2))
		},

		"bitnot": func(b *Block, v any, _ ...any) any {
			return b.Line("~", b.Value(v.(*ast.Node).At(2)))
		},

		"length": func(b *Block, v any, _ ...any) any {
			return b.Line("#", b.Value(v.(*ast.Node).At(2)))
		},

		"not": func(b *Block, v any, _ ...any) any {
			return b.Line("not ", b.Value(v.(*ast.Node).At(2)))
		},

		"self": func(b *Block, v any, _ ...any) any {
			fieldName := luaToString(b.Name(v.(*ast.Node).At(2)))
			if luaKeywords[fieldName] {
				return b.Value(node("chain", "self", node("index", node("string", `"`, fieldName))))
			}
			return "self." + fieldName
		},

		"self_class": func(b *Block, v any, _ ...any) any {
			fieldName := luaToString(b.Name(v.(*ast.Node).At(2)))
			if luaKeywords[fieldName] {
				return b.Value(node("chain", "self", node("dot", "__class"),
					node("index", node("string", `"`, fieldName))))
			}
			return "self.__class." + fieldName
		},

		"self_colon": func(b *Block, v any, _ ...any) any {
			return "self:" + luaToString(b.Name(v.(*ast.Node).At(2)))
		},

		"self_class_colon": func(b *Block, v any, _ ...any) any {
			return "self.__class:" + luaToString(b.Name(v.(*ast.Node).At(2)))
		},

		// a variable reference
		"ref": func(b *Block, v any, _ ...any) any {
			n := v.(*ast.Node)
			if n.At(2) == "super" {
				if sup, ok := b.Get("super").(SuperFn); ok && sup != nil {
					return b.Value(sup(b, nil))
				}
			}
			return luaToString(n.At(2))
		},

		// catch all pure string values
		"raw_value": func(b *Block, v any, _ ...any) any {
			return luaToString(v)
		},
	}
}
