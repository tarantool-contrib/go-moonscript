package format_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/format"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
)

// Builders of trees without positions.

func id(s string) *ast.IdentExpr   { return &ast.IdentExpr{Value: s} }
func str(s string) *ast.StringExpr { return &ast.StringExpr{Value: s} }
func numx(s string) *ast.NumberExpr {
	return &ast.NumberExpr{Value: s}
}
func arith(op string, a, b ast.Expr) *ast.ArithmeticOpExpr {
	return &ast.ArithmeticOpExpr{Operator: op, Lhs: a, Rhs: b}
}
func concatx(a, b ast.Expr) *ast.StringConcatOpExpr {
	return &ast.StringConcatOpExpr{Lhs: a, Rhs: b}
}
func logic(op string, a, b ast.Expr) *ast.LogicalOpExpr {
	return &ast.LogicalOpExpr{Operator: op, Lhs: a, Rhs: b}
}
func rel(op string, a, b ast.Expr) *ast.RelationalOpExpr {
	return &ast.RelationalOpExpr{Operator: op, Lhs: a, Rhs: b}
}
func neg(e ast.Expr) *ast.UnaryMinusOpExpr { return &ast.UnaryMinusOpExpr{Expr: e} }
func not(e ast.Expr) *ast.UnaryNotOpExpr   { return &ast.UnaryNotOpExpr{Expr: e} }
func index(o, k ast.Expr) *ast.AttrGetExpr { return &ast.AttrGetExpr{Object: o, Key: k} }
func call(f ast.Expr, args ...ast.Expr) *ast.FuncCallExpr {
	return &ast.FuncCallExpr{Func: f, Args: args}
}
func method(r ast.Expr, m string, args ...ast.Expr) *ast.FuncCallExpr {
	return &ast.FuncCallExpr{Receiver: r, Method: m, Args: args}
}
func adjusted(e ast.Expr) ast.Expr {
	switch e := e.(type) {
	case *ast.FuncCallExpr:
		e.AdjustRet = true
	case *ast.Comma3Expr:
		e.AdjustRet = true
	}
	return e
}
func fn(stmts ...ast.Stmt) *ast.FunctionExpr {
	return &ast.FunctionExpr{ParList: &ast.ParList{}, Stmts: stmts}
}
func varargFn(stmts ...ast.Stmt) *ast.FunctionExpr {
	return &ast.FunctionExpr{ParList: &ast.ParList{HasVargs: true}, Stmts: stmts}
}
func ret(es ...ast.Expr) *ast.ReturnStmt { return &ast.ReturnStmt{Exprs: es} }
func assign(l []ast.Expr, r ...ast.Expr) *ast.AssignStmt {
	return &ast.AssignStmt{Lhs: l, Rhs: r}
}
func local(names []string, es ...ast.Expr) *ast.LocalAssignStmt {
	return &ast.LocalAssignStmt{Names: names, Exprs: es}
}
func callStmt(c *ast.FuncCallExpr) *ast.FuncCallStmt { return &ast.FuncCallStmt{Expr: c} }
func table(fields ...*ast.Field) *ast.TableExpr      { return &ast.TableExpr{Fields: fields} }
func field(k, v ast.Expr) *ast.Field                 { return &ast.Field{Key: k, Value: v} }

func printNode(t *testing.T, n ast.Element, d parse.Dialect) string {
	t.Helper()
	var b bytes.Buffer
	if err := format.Node(&b, n, format.Options{Dialect: d}); err != nil {
		t.Fatalf("print: %v", err)
	}
	return b.String()
}

// numeralsNormalized: the tree of the text may have other numerals (the
// LuaJIT parser normalizes them, 0x10 to 16), so print differently.
var numeralsNormalized bool

// checkTree prints a statement list, checks the text, and that it parses
// to a tree that prints the same and compiles as the tree does.
func checkTree(t *testing.T, d parse.Dialect, want string, stmts ...ast.Stmt) {
	t.Helper()
	got := printNode(t, &ast.Chunk{Stmts: stmts}, d)
	if got != want {
		t.Errorf("%s: got\n%s\nwant\n%s", dialectName(d), got, want)
		return
	}
	chunk, err := parse.ParseChunk([]byte(got), "test", parse.Options{Dialect: d})
	if err != nil {
		t.Errorf("%s: %q does not parse: %v", dialectName(d), got, err)
		return
	}
	if again := printNode(t, chunk, d); again != got && !numeralsNormalized {
		t.Errorf("%s: the tree of %q prints as %q", dialectName(d), got, again)
	}
	p1, err1 := luaCompile(stmts, d)
	p2, err2 := luaCompile(chunk.Stmts, d)
	if err1 != nil || err2 != nil {
		t.Errorf("%s: compile: %v, %v", dialectName(d), err1, err2)
		return
	}
	if diff := protoDiff(p1, p2, "main"); diff != "" {
		t.Errorf("%s: %q compiles to other code than its tree: %s", dialectName(d), got, diff)
	}
}

// checkExpr checks e printed in "x = e".
func checkExpr(t *testing.T, want string, e ast.Expr) {
	t.Helper()
	for _, d := range dialects {
		checkTree(t, d, "x = "+want+"\n", assign([]ast.Expr{id("x")}, e))
	}
}

func TestPrintPrecedence(t *testing.T) {
	a, b, c := id("a"), id("b"), id("c")
	cases := []struct {
		e    ast.Expr
		want string
	}{
		{arith("*", arith("+", a, b), c), "(a + b) * c"},
		{arith("+", a, arith("*", b, c)), "a + b * c"},
		{arith("-", arith("-", a, b), c), "a - b - c"},
		{arith("-", a, arith("-", b, c)), "a - (b - c)"},
		{arith("/", a, arith("*", b, c)), "a / (b * c)"},
		{concatx(a, concatx(b, c)), "a .. b .. c"},
		{concatx(concatx(a, b), c), "(a .. b) .. c"},
		{arith("^", a, arith("^", b, c)), "a ^ b ^ c"},
		{arith("^", arith("^", a, b), c), "(a ^ b) ^ c"},
		{neg(arith("^", a, numx("2"))), "-a ^ 2"},
		{arith("^", neg(a), numx("2")), "(-a) ^ 2"},
		{arith("^", numx("2"), neg(a)), "2 ^ -a"},
		{arith("^", numx("2"), neg(arith("^", a, b))), "2 ^ -a ^ b"},
		{arith("*", arith("^", a, neg(b)), c), "a ^ -b * c"},
		{not(rel("==", a, b)), "not (a == b)"},
		{rel("==", not(a), b), "not a == b"},
		{rel("<", rel("<", a, b), c), "a < b < c"},
		{rel("<", a, rel("<", b, c)), "a < (b < c)"},
		{logic("or", logic("and", a, b), c), "a and b or c"},
		{logic("and", a, logic("or", b, c)), "a and (b or c)"},
		{logic("or", a, logic("or", b, c)), "a or (b or c)"},
		{concatx(arith("+", a, b), c), "a + b .. c"},
		{arith("+", a, concatx(b, c)), "a + (b .. c)"},
		{neg(neg(a)), "- -a"},
		{neg(numx("1")), "-1"},
		{neg(neg(numx("1"))), "- -1"},
		{arith("-", a, neg(b)), "a - -b"},
		{&ast.UnaryLenOpExpr{Expr: neg(a)}, "#-a"},
		{not(not(a)), "not not a"},
	}
	for _, c := range cases {
		checkExpr(t, c.want, c.e)
	}
}

func TestPrintPrefix(t *testing.T) {
	a := id("a")
	cases := []struct {
		e    ast.Expr
		want string
	}{
		{method(str("x"), "rep", numx("2")), `("x"):rep(2)`},
		{index(table(), str("x")), "({}).x"},
		{call(fn()), "(function() end)()"},
		{call(logic("or", id("f"), id("g"))), "(f or g)()"},
		{index(numx("1"), str("x")), "(1).x"},
		{index(neg(a), str("x")), "(-a).x"},
		{index(call(a), str("x")), "a().x"},
		{call(index(index(a, str("b")), str("c"))), "a.b.c()"},
	}
	for _, c := range cases {
		checkExpr(t, c.want, c.e)
	}
	// "..." in a function
	for _, d := range dialects {
		checkTree(t, d, "f = function(...)\n    return (...).x\nend\n",
			assign([]ast.Expr{id("f")}, varargFn(ret(index(&ast.Comma3Expr{}, str("x"))))))
	}
}

func TestPrintAdjustRet(t *testing.T) {
	f, g := id("f"), id("g")
	for _, d := range dialects {
		checkTree(t, d, "return (f())\n", ret(adjusted(call(f))))
		checkTree(t, d, "return f(), (g())\n", ret(call(f), adjusted(call(g))))
		checkTree(t, d, "return g(), f()\n", ret(adjusted(call(g)), call(f)))
		checkTree(t, d, "local a, b = (f())\n", local([]string{"a", "b"}, adjusted(call(f))))
		checkTree(t, d, "f((g()))\n", callStmt(call(f, adjusted(call(g)))))
		checkTree(t, d, "f(g(), 1)\n", callStmt(call(f, adjusted(call(g)), numx("1"))))
		checkTree(t, d, "t = {(f())}\n", assign([]ast.Expr{id("t")}, table(field(nil, adjusted(call(f))))))
		checkTree(t, d, "t = {f(), a = g()}\n",
			assign([]ast.Expr{id("t")}, table(field(nil, call(f)), field(str("a"), adjusted(call(g))))))
		checkTree(t, d, "f = function(...)\n    return (...)\nend\n",
			assign([]ast.Expr{f}, varargFn(ret(adjusted(&ast.Comma3Expr{})))))
		// a call statement and a target take no parentheses
		checkTree(t, d, "f()\n", callStmt(adjusted(call(f)).(*ast.FuncCallExpr)))
	}
}

func TestPrintKeys(t *testing.T) {
	tt := id("t")
	for _, d := range dialects {
		checkTree(t, d, "x = t.b\n", assign([]ast.Expr{id("x")}, index(tt, str("b"))))
		checkTree(t, d, "x = t[\"end\"]\n", assign([]ast.Expr{id("x")}, index(tt, str("end"))))
		checkTree(t, d, "x = t[\"a-b\"]\n", assign([]ast.Expr{id("x")}, index(tt, str("a-b"))))
		checkTree(t, d, "x = t[\"1a\"]\n", assign([]ast.Expr{id("x")}, index(tt, str("1a"))))
		checkTree(t, d, "x = t[\"\"]\n", assign([]ast.Expr{id("x")}, index(tt, str(""))))
		checkTree(t, d, "x = t[1]\n", assign([]ast.Expr{id("x")}, index(tt, numx("1"))))
		checkTree(t, d, "t.b = 1\n", assign([]ast.Expr{index(tt, str("b"))}, numx("1")))
		checkTree(t, d, "x = {b = 1, [\"end\"] = 2, [1] = 3, [\"1\"] = 4, 5}\n", assign([]ast.Expr{id("x")},
			table(field(str("b"), numx("1")), field(str("end"), numx("2")), field(numx("1"), numx("3")),
				field(str("1"), numx("4")), field(nil, numx("5")))))
		checkTree(t, d, "function a.b.c:d() end\n", &ast.FuncDefStmt{
			Name: &ast.FuncName{Receiver: index(index(id("a"), str("b")), str("c")), Method: "d"}, Func: fn()})
	}
	// goto is a keyword of LuaJIT only
	checkTree(t, parse.Lua51, "x = t.goto\n", assign([]ast.Expr{id("x")}, index(tt, str("goto"))))
	checkTree(t, parse.LuaJIT, "x = t[\"goto\"]\n", assign([]ast.Expr{id("x")}, index(tt, str("goto"))))
}

func TestPrintStrings(t *testing.T) {
	cases := []struct{ value, want string }{
		{"abc", `"abc"`},
		{`say "hi"`, `'say "hi"'`},
		{`it's`, `"it's"`},
		{`'"`, `"'\""`},
		{"a\\b", `"a\\b"`},
		{"\a\b\f\n\r\t\v", `"\a\b\f\n\r\t\v"`},
		{"\x00", `"\0"`},
		{"\x001", `"\0001"`},
		{"\x01\x1f\x7f", `"\1\31\127"`},
		{"\x7f9", `"\1279"`},
		{"\xff\xfe", `"\255\254"`},
		{"\xe2\x82\xac", "\"€\""},  // valid UTF-8 is kept
		{"\xe2\x82", `"\226\130"`}, // a truncated sequence is not
		{"]]", `"]]"`},
		{"--[[", `"--[["`},
	}
	for _, c := range cases {
		checkExpr(t, c.want, str(c.value))
	}
	var b bytes.Buffer
	if err := format.Node(&b, str("x"), format.Options{Quotes: format.QuotesSingle}); err != nil || b.String() != "'x'" {
		t.Errorf("single quotes: %q, %v", b.String(), err)
	}
}

func TestPrintNumbers(t *testing.T) {
	cases := []struct{ value, want string }{
		{"1", "1"},
		{"0.5", "0.5"},
		{"1e+100", "1e+100"},
		{"5e-324", "5e-324"},
		{"0x10", "0x10"},
		{"+Inf", "1e999"},
		{"-Inf", "-1e999"},
		{"NaN", "0 / 0"},
		{"-0", "-0"},
		{"-2", "-2"},
		{" 12 ", "12"},
		{"1e400", "1e400"},
	}
	numeralsNormalized = true
	defer func() { numeralsNormalized = false }()
	for _, c := range cases {
		checkExpr(t, c.want, numx(c.value))
	}
	// a negative number is a unary minus, NaN a division
	checkExpr(t, "(-2) ^ 2", arith("^", numx("-2"), numx("2")))
	checkExpr(t, "2 ^ (0 / 0)", arith("^", numx("2"), numx("nan")))
	checkExpr(t, "a - -1", arith("-", id("a"), numx("-1")))
	// numerals next to "..": spaces, never "1..2"
	checkExpr(t, "1 .. 2", concatx(numx("1"), numx("2")))
	checkExpr(t, "a .. .5", concatx(id("a"), numx(".5")))
}

// TestPrintStatementAmbiguity checks the ";" before a statement starting
// with "(", which would else be the arguments of a call ending the
// statement before it ("f\n(g)").
func TestPrintStatementAmbiguity(t *testing.T) {
	fg := logic("or", id("f"), id("g"))
	for _, d := range dialects {
		checkTree(t, d, "x = y;\n(f or g)()\n", assign([]ast.Expr{id("x")}, id("y")), callStmt(call(fg)))
		checkTree(t, d, "f();\n(\"s\").x = 1\n", callStmt(call(id("f"))),
			assign([]ast.Expr{index(str("s"), str("x"))}, numx("1")))
		checkTree(t, d, "(f or g)()\n", callStmt(call(fg)))
		checkTree(t, d, "do\n    (f or g)()\nend\n", &ast.DoBlockStmt{Stmts: []ast.Stmt{callStmt(call(fg))}})
		checkTree(t, d, "local a = f;\n(g or f)(a)\n", local([]string{"a"}, id("f")),
			callStmt(call(logic("or", id("g"), id("f")), id("a"))))
	}
}

func TestPrintStatements(t *testing.T) {
	for _, d := range dialects {
		checkTree(t, d, "local a, b = 1, 2\n", local([]string{"a", "b"}, numx("1"), numx("2")))
		checkTree(t, d, "local a\n", local([]string{"a"}))
		checkTree(t, d, "local function f()\n    return f\nend\n",
			&ast.LocalAssignStmt{Names: []string{"f"}, Exprs: []ast.Expr{fn(ret(id("f")))}, IsFunction: true})
		checkTree(t, d, "local f = function()\n    return f\nend\n",
			local([]string{"f"}, fn(ret(id("f")))))
		checkTree(t, d, "if a then\n    b()\nelseif c then\n    d()\nelse\n    e()\nend\n", &ast.IfStmt{
			Condition: id("a"), Then: []ast.Stmt{callStmt(call(id("b")))},
			Else: []ast.Stmt{&ast.IfStmt{Condition: id("c"), Then: []ast.Stmt{callStmt(call(id("d")))},
				Else: []ast.Stmt{callStmt(call(id("e")))}}}})
		checkTree(t, d, "for i = 1, 10, 2 do end\n", &ast.NumberForStmt{Name: "i", Init: numx("1"), Limit: numx("10"), Step: numx("2")})
		checkTree(t, d, "for k, v in pairs(t) do\n    break\nend\n", &ast.GenericForStmt{Names: []string{"k", "v"},
			Exprs: []ast.Expr{call(id("pairs"), id("t"))}, Stmts: []ast.Stmt{&ast.BreakStmt{}}})
		checkTree(t, d, "while true do end\n", &ast.WhileStmt{Condition: &ast.TrueExpr{}})
		checkTree(t, d, "repeat until false\n", &ast.RepeatStmt{Condition: &ast.FalseExpr{}})
		checkTree(t, d, "a, b = b, a\n", assign([]ast.Expr{id("a"), id("b")}, id("b"), id("a")))
		checkTree(t, d, "o:m(1)\n", callStmt(method(id("o"), "m", numx("1"))))
	}
	checkTree(t, parse.LuaJIT, "goto l\n::l::\n", &ast.GotoStmt{Label: "l"}, &ast.LabelStmt{Name: "l"})
}

func TestPrintErrors(t *testing.T) {
	cases := []struct {
		name string
		n    ast.Element
		d    parse.Dialect
	}{
		{"keyword name", id("end"), parse.Lua51},
		{"goto under LuaJIT", id("goto"), parse.LuaJIT},
		{"empty name", id(""), parse.Lua51},
		{"method name", method(id("o"), "a-b"), parse.Lua51},
		{"assignment to a call", assign([]ast.Expr{call(id("f"))}, numx("1")), parse.Lua51},
		{"nil operand", arith("+", id("a"), nil), parse.Lua51},
		{"unknown operator", arith("//", id("a"), id("b")), parse.Lua51},
		{"local name", local([]string{"a b"}), parse.Lua51},
		{"goto under Lua 5.1", &ast.GotoStmt{Label: "l"}, parse.Lua51},
		{"function name", &ast.FuncDefStmt{Name: &ast.FuncName{Func: index(id("a"), str("b-c"))}, Func: fn()}, parse.Lua51},
		{"call statement of a name", &ast.FuncCallStmt{Expr: id("f")}, parse.Lua51},
		{"nil", nil, parse.Lua51},
	}
	for _, c := range cases {
		var b bytes.Buffer
		err := format.Node(&b, c.n, format.Options{Dialect: c.d})
		if err == nil {
			t.Errorf("%s: no error, printed %q", c.name, b.String())
		} else if !strings.HasPrefix(err.Error(), "format: ") {
			t.Errorf("%s: error %q", c.name, err)
		}
	}
}
