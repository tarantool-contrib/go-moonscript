package format_test

import (
	"fmt"
	"os"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/format"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
)

func ExampleSource() {
	src := []byte(`local M={}  -- the module
function M.greet(name,greeting)
  greeting=greeting or 'Hello'


  return greeting..", "..name..'!'
end
local cfg = {
  listen=3301, ['log level']=5,
}
if not cfg then return end
return M`)
	out, err := format.Source(src, format.Options{Dialect: parse.LuaJIT})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Print(string(out))
	// Output:
	// local M = {} -- the module
	// function M.greet(name, greeting)
	//     greeting = greeting or 'Hello'
	//
	//     return greeting .. ", " .. name .. '!'
	// end
	// local cfg = {
	//     listen = 3301,
	//     ['log level'] = 5,
	// }
	// if not cfg then return end
	// return M
}

// A tree built by code is printed with the parentheses, keys and literals
// it needs.
func ExampleNode() {
	// return (a + b) * -c, t["end"], (f())
	tree := &ast.ReturnStmt{Exprs: []ast.Expr{
		&ast.ArithmeticOpExpr{Operator: "*",
			Lhs: &ast.ArithmeticOpExpr{Operator: "+", Lhs: &ast.IdentExpr{Value: "a"}, Rhs: &ast.IdentExpr{Value: "b"}},
			Rhs: &ast.UnaryMinusOpExpr{Expr: &ast.IdentExpr{Value: "c"}}},
		&ast.AttrGetExpr{Object: &ast.IdentExpr{Value: "t"}, Key: &ast.StringExpr{Value: "end"}},
		&ast.FuncCallExpr{Func: &ast.IdentExpr{Value: "f"}, AdjustRet: true},
	}}
	if err := format.Node(os.Stdout, tree, format.Options{}); err != nil {
		fmt.Println(err)
	}
	fmt.Println()
	// Output:
	// return (a + b) * -c, t["end"], (f())
}

func ExampleNewCommentMap() {
	src := []byte(`-- the answer
local x = 42 -- to everything
local t = { -- empty
}
`)
	chunk, err := parse.ParseChunk(src, "example", parse.Options{Tokens: true})
	if err != nil {
		fmt.Println(err)
		return
	}
	cm := format.NewCommentMap(chunk)
	for _, s := range chunk.Stmts {
		for _, c := range cm[s] {
			fmt.Printf("%q: %s of the statement on line %d\n", c.Raw, c.Place, chunk.Pos(s.Span().Start).Line)
		}
	}
	tbl := chunk.Stmts[1].(*ast.LocalAssignStmt).Exprs[0]
	for _, c := range cm[tbl] {
		fmt.Printf("%q: %s of the table, before %v\n", c.Raw, c.Place, c.Next)
	}
	// Output:
	// "-- the answer": leading of the statement on line 2
	// "-- to everything": trailing of the statement on line 2
	// "-- empty": inner of the table, before }
}

// Chunk prints a transformed tree with the comments of its source: here a
// statement is added before the others and one is removed.
func ExampleChunk() {
	src := []byte(`local a = 1 -- one
local b = 2 -- two
print(a, b)
`)
	chunk, err := parse.ParseChunk(src, "example", parse.Options{Tokens: true})
	if err != nil {
		fmt.Println(err)
		return
	}
	cm := format.NewCommentMap(chunk)
	delete(cm, chunk.Stmts[1]) // its comments go with it
	strict := &ast.FuncCallStmt{Expr: &ast.FuncCallExpr{
		Func: &ast.IdentExpr{Value: "require"},
		Args: []ast.Expr{&ast.StringExpr{Value: "strict"}},
	}}
	cm[strict] = []*format.Comment{{Comment: ast.Comment{Text: " added"}, Place: format.Trailing, SameLine: true}}
	chunk.Stmts = []ast.Stmt{strict, chunk.Stmts[0], chunk.Stmts[2]}
	if err := format.Chunk(os.Stdout, chunk, cm, format.Options{}); err != nil {
		fmt.Println(err)
	}
	// Output:
	// require("strict") -- added
	// local a = 1 -- one
	// print(a, b)
}
