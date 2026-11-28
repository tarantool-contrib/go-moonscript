package compiler

// Port of moonscript/compile/statement.moon.

import (
	"strings"

	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
)

// StatementCompiler compiles a statement node; returns nil, string, *Line
// or *Block.
type StatementCompiler func(b *Block, n *ast.Node, extra ...any) any

var statementCompilers map[string]StatementCompiler

func init() {
	statementCompilers = map[string]StatementCompiler{
		"raw": func(b *Block, n *ast.Node, _ ...any) any {
			b.Add(n.At(2))
			return nil
		},

		// tags existing bindings as constant, has no output. only generated
		// by transformations, there is no syntax that produces this statement
		"declare_constants": func(b *Block, n *ast.Node, _ ...any) any {
			for _, name := range n.At(2).(*ast.Node).Items {
				if ntype(name) == "ref" {
					name = name.(*ast.Node).At(2)
				}
				b.PutName(name, &ConstBinding{Pos: n.Pos})
			}
			return nil
		},

		"declare": func(b *Block, n *ast.Node, _ ...any) any {
			undeclared := b.Declare(n.At(2).(*ast.Node))
			if len(undeclared) > 0 {
				l := b.Line("local ")
				l.AppendList(stringsToAny(undeclared), ", ")
				return l
			}
			return nil
		},

		// this overrides the existing names with new locals, used for local
		// keyword
		"declare_with_shadows": func(b *Block, n *ast.Node, _ ...any) any {
			names := n.At(2).(*ast.Node)
			b.Declare(names)
			b.PutFreshNames(names)
			l := b.Line("local ")
			var list []any
			for _, name := range names.Items {
				list = append(list, b.Name(name))
			}
			l.AppendList(list, ", ")
			return l
		},

		"assign": func(b *Block, n *ast.Node, _ ...any) any {
			names, values := n.At(2).(*ast.Node), n.At(3).(*ast.Node)

			undeclared := b.Declare(names)
			declare := "local " + strings.Join(undeclared, ", ")

			hasFndef := false
			for _, v := range values.Items {
				if ntype(v) == "fndef" {
					hasFndef = true
				}
			}

			l := b.Line()
			if len(undeclared) == names.Len() && !hasFndef {
				l.Append(declare)
			} else {
				if len(undeclared) > 0 {
					b.Add(declare, n.Pos)
				}
				l.AppendList(b.valueList(names.Items), ", ")
			}
			l.Append(" = ")
			l.AppendList(b.valueList(values.Items), ", ")
			return l
		},

		"return": func(b *Block, n *ast.Node, _ ...any) any {
			if s, ok := n.At(2).(string); ok && s == "" {
				return b.Line("return ", nil)
			}
			return b.Line("return ", b.Value(n.At(2)))
		},

		"break": func(b *Block, n *ast.Node, _ ...any) any {
			return "break"
		},

		"if": func(b *Block, n *ast.Node, _ ...any) any {
			cond, block := n.At(2), n.At(3).(*ast.Node)
			root := b.Block(b.Line("if ", b.Value(cond), " then"), nil)
			root.Stms(block)

			current := root
			addClause := func(clause *ast.Node) {
				var next *Block
				i := 2
				if clause.Kind() == "else" {
					next = b.Block("else", nil)
				} else {
					i++
					next = b.Block(b.Line("elseif ", b.Value(clause.At(2)), " then"), nil)
				}
				next.Stms(clause.At(i).(*ast.Node))
				current.Next = next
				current = next
			}
			for i := 4; i <= n.Len(); i++ {
				addClause(n.At(i).(*ast.Node))
			}
			return root
		},

		"repeat": func(b *Block, n *ast.Node, _ ...any) any {
			cond, block := n.At(2), n.At(3).(*ast.Node)
			blk := b.Block("repeat", b.Line("until ", b.Value(cond)))
			blk.Stms(block)
			return blk
		},

		"while": func(b *Block, n *ast.Node, _ ...any) any {
			cond, block := n.At(2), n.At(3).(*ast.Node)
			blk := b.Block(b.Line("while ", b.Value(cond), " do"), nil)
			blk.Stms(block)
			return blk
		},

		"for": func(b *Block, n *ast.Node, _ ...any) any {
			name, bounds, block := n.At(2), n.At(3).(*ast.Node), n.At(4).(*ast.Node)
			loop := b.Line("for ", b.Name(name), " = ",
				b.Value(ast.New(append([]any{"explist"}, bounds.Items...)...)), " do")
			blk := b.Block(loop, nil)
			blk.PutFreshNames(node(name))
			blk.Stms(block)
			return blk
		},

		// for x in y ...
		// {"foreach", {names...}, {exp...}, body}
		"foreach": func(b *Block, n *ast.Node, _ ...any) any {
			names, exps, block := n.At(2).(*ast.Node), n.At(3).(*ast.Node), n.At(4).(*ast.Node)

			loop := b.Line()
			loop.Append("for ")

			blk := b.Block(loop, nil)
			var nameList []any
			for _, name := range names.Items {
				nameList = append(nameList, blk.Name(name, false))
			}
			loop.AppendList(nameList, ", ")
			loop.Append(" in ")
			loop.AppendList(b.valueList(exps.Items), ",")
			loop.Append(" do")

			blk.PutFreshNames(names)
			blk.Stms(block)
			return blk
		},

		"export": func(b *Block, n *ast.Node, _ ...any) any {
			switch names := n.At(2).(type) {
			case string:
				switch names {
				case "*":
					b.ExportAll = true
				case "^":
					b.ExportProper = true
				}
			case *ast.Node:
				b.Declare(names)
			}
			return nil
		},

		"group": func(b *Block, n *ast.Node, _ ...any) any {
			// pass the position to any children that don't have their own.
			// NOOP is a shared constant so it must not be tagged
			body := n.At(2).(*ast.Node)
			if n.Pos != 0 {
				for _, child := range body.Items {
					if isTable(child) && posOf(child) == 0 && ntype(child) != "noop" {
						setPos(child, n.Pos)
					}
				}
			}
			b.Stms(body)
			return nil
		},

		"do": func(b *Block, n *ast.Node, _ ...any) any {
			blk := b.Block(nil, nil)
			blk.Stms(n.At(2).(*ast.Node))
			return blk
		},

		"noop": func(b *Block, n *ast.Node, _ ...any) any { return nil },
	}
}

func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
