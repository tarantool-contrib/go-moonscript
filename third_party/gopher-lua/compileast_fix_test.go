package lua

// go-moonscript patch: CompileAST and LState.LoadAST (compileast.go): a
// parsed tree loads as its source does (the same dump), a tree built by
// hand is checked (errors at its nodes' lines), lines a tree lacks, the
// tree left as it is. The fuzz test is in compileast_fuzz_fix_test.go,
// the luasuites sources' round trip in internal/luasuites.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
)

// parseDialect is the parser's dialect of a state's.
func parseDialect(d Dialect) parse.Dialect {
	if d == DialectLuaJIT {
		return parse.LuaJIT
	}
	return parse.Lua51
}

// loadASTFromSource parses code as LoadChunk does and loads the tree with
// LoadAST.
func loadASTFromSource(L *LState, code []byte, chunkname string) (*LFunction, error) {
	d := L.Dialect()
	chunk, err := parse.ParseBytes(code, syntaxChunkID(chunkname, d), parseDialect(d))
	if err != nil {
		return nil, err
	}
	return L.LoadAST(chunk, chunkname)
}

// roundTripFiles are the Lua sources of the round trip: the compat and
// dialect scripts and the other test scripts.
func roundTripFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, pat := range []string{"testdata/*.lua", "testdata/compat/*.lua", "testdata/compat/files/*.lua", "testdata/dialect/*.lua"} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) < 100 {
		t.Fatalf("only %d files", len(files))
	}
	return files
}

// checkRoundTrip loads src with LoadChunk and its parsed tree with LoadAST
// under the dialect, and fails unless they give the same dump, or both an
// error. It reports whether the source loaded.
func checkRoundTrip(t *testing.T, src []byte, chunkname string, d Dialect) bool {
	t.Helper()
	L := NewState(Options{Dialect: d, SkipOpenLibs: true})
	defer L.Close()
	fnSrc, errSrc := L.LoadChunk(bytes.NewReader(src), chunkname)
	chunk, perr := parse.ParseBytes(src, syntaxChunkID(chunkname, d), parseDialect(d))
	if perr != nil {
		if errSrc == nil {
			t.Errorf("%s (%v): loads, but does not parse: %v", chunkname, d, perr)
		}
		return false
	}
	// a parsed tree has every line
	c := &astCheck{dialect: d, source: chunkname, line: 1}
	c.chunk(chunk)
	if c.unlined {
		t.Errorf("%s (%v): a parsed node has no line", chunkname, d)
	}
	fnAST, errAST := L.LoadAST(chunk, chunkname)
	switch {
	case errSrc != nil && errAST != nil:
		return false
	case errSrc != nil:
		// a check of source loading only: the model of lua5.1's registers
		var ae *ApiError
		var pe *parse.Error
		if !errors.As(errSrc, &ae) || !errors.As(ae.Cause, &pe) || d != DialectLua51 ||
			pe.Message != "function or expression too complex" {
			t.Errorf("%s (%v): LoadChunk fails (%v), LoadAST does not", chunkname, d, errSrc)
		}
		return false
	case errAST != nil:
		t.Errorf("%s (%v): LoadAST fails: %v", chunkname, d, errAST)
		return false
	}
	dumpSrc, err1 := DumpFunction(fnSrc)
	dumpAST, err2 := DumpFunction(fnAST)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if !bytes.Equal(dumpSrc, dumpAST) {
		t.Errorf("%s (%v): LoadAST's dump differs from LoadChunk's", chunkname, d)
	}
	return true
}

func TestCompileASTRoundTrip(t *testing.T) {
	loaded := 0
	for _, file := range roundTripFiles(t) {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range []Dialect{DialectLua51, DialectLuaJIT} {
			if checkRoundTrip(t, src, "@"+filepath.Base(file), d) {
				loaded++
			}
		}
	}
	// and as a string chunk, whose chunk name is the source
	src := []byte("local t = {...}\nreturn #t, function(...) return arg end")
	for _, d := range []Dialect{DialectLua51, DialectLuaJIT} {
		if checkRoundTrip(t, src, string(src), d) {
			loaded++
		}
	}
	t.Logf("%d loads compared", loaded)
}

// TestCompileASTSourceOnly: what only source loading checks. A call with
// 252 arguments needs more registers than lua5.1 has (MAXSTACK 250, the
// parser's model of its registers), fewer than the compiler's limit (256):
// LoadChunk rejects it with lua5.1's message, LoadAST compiles its tree.
// Under DialectLuaJIT the parser's model of LuaJIT's slots rejects the
// source.
func TestCompileASTSourceOnly(t *testing.T) {
	src := []byte("f(" + strings.TrimSuffix(strings.Repeat("1,", 252), ",") + ")")
	L := NewState(Options{SkipOpenLibs: true})
	defer L.Close()
	if _, err := L.LoadChunk(bytes.NewReader(src), "=src"); err == nil || err.Error() != "src:1: function or expression too complex near '1'" {
		t.Errorf("LoadChunk: %v", err)
	}
	if _, err := loadASTFromSource(L, src, "=src"); err != nil {
		t.Errorf("LoadAST: %v", err)
	}
	checkRoundTrip(t, src, "=src", DialectLua51)
	if _, err := parse.ParseBytes(src, "src", parse.LuaJIT); err == nil {
		t.Error("parsed under LuaJIT")
	}
}

// TestCompatAST runs the compat scripts loaded with LoadAST: their output
// is the reference's, as with LoadChunk (TestCompat).
func TestCompatAST(t *testing.T) {
	scripts, err := filepath.Glob("testdata/compat/*.lua")
	if err != nil {
		t.Fatal(err)
	}
	for _, script := range scripts {
		name := strings.TrimSuffix(filepath.Base(script), ".lua")
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("testdata/compat/ref", name+".out"))
			if err != nil {
				t.Fatal(err)
			}
			if got := runCompatScriptWith(t, script, loadASTFromSource); got != string(want) {
				t.Errorf("output differs from the reference's:\n--- got\n%s\n--- want\n%s", got, want)
			}
		})
	}
}

/* building trees */

func at[T ast.PositionHolder](n T, line int) T {
	n.SetLine(line)
	return n
}

func ident(name string) *ast.IdentExpr { return &ast.IdentExpr{Value: name} }

func num(v string) *ast.NumberExpr { return &ast.NumberExpr{Value: v} }

func str(v string) *ast.StringExpr { return &ast.StringExpr{Value: v} }

func call(fn ast.Expr, args ...ast.Expr) *ast.FuncCallExpr {
	return &ast.FuncCallExpr{Func: fn, Args: args}
}

func callStmt(fn ast.Expr, args ...ast.Expr) *ast.FuncCallStmt {
	return &ast.FuncCallStmt{Expr: call(fn, args...)}
}

func function(names []string, vararg bool, body ...ast.Stmt) *ast.FunctionExpr {
	return &ast.FunctionExpr{ParList: &ast.ParList{Names: names, HasVargs: vararg}, Stmts: body}
}

func local(names []string, exprs ...ast.Expr) *ast.LocalAssignStmt {
	return &ast.LocalAssignStmt{Names: names, Exprs: exprs}
}

func ret(exprs ...ast.Expr) *ast.ReturnStmt { return &ast.ReturnStmt{Exprs: exprs} }

// unknownStmt and unknownExpr are nodes of types the compiler doesn't know.
type unknownStmt struct{ ast.StmtBase }

type unknownExpr struct{ ast.ExprBase }

// nested is n levels of "not" around e.
func nested(n int, e ast.Expr) ast.Expr {
	for range n {
		e = &ast.UnaryNotOpExpr{Expr: e}
	}
	return e
}

// manyLocals declares n locals, one statement each.
func manyLocals(n int) []ast.Stmt {
	var stmts []ast.Stmt
	for i := range n {
		stmts = append(stmts, local([]string{fmt.Sprintf("v%d", i)}))
	}
	return stmts
}

// manyUpvalues is a function at line 9 referencing n locals of the main
// function.
func manyUpvalues(n int) []ast.Stmt {
	stmts := manyLocals(n)
	var args []ast.Expr
	for i := range n {
		args = append(args, ident(fmt.Sprintf("v%d", i)))
	}
	return append(stmts, ret(at(function(nil, false, callStmt(ident("f"), args...)), 9)))
}

func TestCompileASTErrors(t *testing.T) {
	cyclic := &ast.DoBlockStmt{}
	cyclic.Stmts = []ast.Stmt{cyclic}
	var nilCall *ast.FuncCallExpr
	var nilIf *ast.IfStmt
	cases := []struct {
		name    string
		dialect Dialect
		chunk   []ast.Stmt
		want    string // the error, "" for none
	}{
		{"nil statement", DialectLua51, []ast.Stmt{nil}, "x:1: nil statement"},
		{"typed nil statement", DialectLua51, []ast.Stmt{at(callStmt(ident("f")), 3), nilIf}, "x:3: nil IfStmt"},
		{"unknown statement", DialectLua51, []ast.Stmt{&unknownStmt{}}, "x:1: unknown statement type *lua.unknownStmt"},
		{"unknown expression", DialectLua51, []ast.Stmt{ret(&unknownExpr{})}, "x:1: unknown expression type *lua.unknownExpr"},
		{"nil value", DialectLua51, []ast.Stmt{at(ret(nil), 4)}, "x:4: nil value"},
		{"nil condition", DialectLua51, []ast.Stmt{&ast.WhileStmt{}}, "x:1: nil condition"},
		{"nil operand", DialectLua51, []ast.Stmt{ret(at(&ast.ArithmeticOpExpr{Operator: "+", Lhs: num("1")}, 2))}, "x:2: nil operand"},
		{"nil field", DialectLua51, []ast.Stmt{ret(&ast.TableExpr{Fields: []*ast.Field{nil}})}, "x:1: nil table field"},
		{"nil field value", DialectLua51, []ast.Stmt{ret(&ast.TableExpr{Fields: []*ast.Field{{Key: str("k")}}})}, "x:1: nil value"},
		{"typed nil call", DialectLua51, []ast.Stmt{&ast.FuncCallStmt{Expr: nilCall}}, "x:1: nil FuncCallExpr"},
		{"nil parameter list", DialectLua51, []ast.Stmt{ret(&ast.FunctionExpr{})}, "x:1: function without a parameter list"},
		{"nil function name", DialectLua51, []ast.Stmt{&ast.FuncDefStmt{Func: function(nil, false)}}, "x:1: function statement without a name"},
		{"nil function", DialectLua51, []ast.Stmt{&ast.FuncDefStmt{Name: &ast.FuncName{Func: ident("f")}}}, "x:1: function statement without a function"},
		{"both name and receiver", DialectLua51, []ast.Stmt{&ast.FuncDefStmt{Name: &ast.FuncName{Func: ident("f"), Receiver: ident("t"), Method: "m"}, Func: function(nil, false)}},
			"x:1: function name with both a target and a receiver"},
		{"function name not assignable", DialectLua51, []ast.Stmt{&ast.FuncDefStmt{Name: &ast.FuncName{Func: str("f")}, Func: function(nil, false)}},
			"x:1: cannot assign to StringExpr"},
		{"bad arithmetic operator", DialectLua51, []ast.Stmt{ret(&ast.ArithmeticOpExpr{Operator: "//", Lhs: num("1"), Rhs: num("2")})}, "x:1: unknown arithmetic operator '//'"},
		{"bad relational operator", DialectLua51, []ast.Stmt{ret(&ast.RelationalOpExpr{Operator: "!=", Lhs: num("1"), Rhs: num("2")})}, "x:1: unknown relational operator '!='"},
		{"bad logical operator", DialectLua51, []ast.Stmt{ret(&ast.LogicalOpExpr{Operator: "xor", Lhs: num("1"), Rhs: num("2")})}, "x:1: unknown logical operator 'xor'"},
		{"assign to a call", DialectLua51, []ast.Stmt{&ast.AssignStmt{Lhs: []ast.Expr{call(ident("f"))}, Rhs: []ast.Expr{num("1")}}}, "x:1: cannot assign to FuncCallExpr"},
		{"assign to a constant", DialectLua51, []ast.Stmt{&ast.AssignStmt{Lhs: []ast.Expr{num("1")}, Rhs: []ast.Expr{num("1")}}}, "x:1: cannot assign to NumberExpr"},
		{"assign without values", DialectLua51, []ast.Stmt{&ast.AssignStmt{Lhs: []ast.Expr{ident("a")}}}, "x:1: assignment without values"},
		{"assign without targets", DialectLua51, []ast.Stmt{&ast.AssignStmt{Rhs: []ast.Expr{num("1")}}}, "x:1: assignment without targets"},
		{"local without names", DialectLua51, []ast.Stmt{local(nil, num("1"))}, "x:1: local declaration without names"},
		{"for without names", DialectLua51, []ast.Stmt{&ast.GenericForStmt{Exprs: []ast.Expr{ident("t")}}}, "x:1: for statement without names"},
		{"for without expressions", DialectLua51, []ast.Stmt{&ast.GenericForStmt{Names: []string{"k"}}}, "x:1: for statement without expressions"},
		{"numeric for without limit", DialectLua51, []ast.Stmt{&ast.NumberForStmt{Name: "i", Init: num("1")}}, "x:1: nil limit"},
		{"call statement of a name", DialectLua51, []ast.Stmt{&ast.FuncCallStmt{Expr: ident("f")}}, "x:1: call statement without a call (IdentExpr)"},
		{"call without function", DialectLua51, []ast.Stmt{&ast.FuncCallStmt{Expr: &ast.FuncCallExpr{}}}, "x:1: call without a function"},
		{"call with both", DialectLua51, []ast.Stmt{&ast.FuncCallStmt{Expr: &ast.FuncCallExpr{Func: ident("f"), Receiver: ident("o"), Method: "m"}}},
			"x:1: call with both a function and a receiver"},
		{"malformed number", DialectLua51, []ast.Stmt{ret(at(num("1x"), 7))}, "x:7: malformed number '1x'"},
		{"cdata numeral under Lua51", DialectLua51, []ast.Stmt{ret(&ast.CdataNumberExpr{Value: "1LL"})}, "x:1: cdata numeral '1LL' needs DialectLuaJIT"},
		{"cdata numeral under LuaJIT", DialectLuaJIT, []ast.Stmt{ret(&ast.CdataNumberExpr{Value: "1LL"})}, ""},
		{"empty cdata numeral", DialectLuaJIT, []ast.Stmt{ret(&ast.CdataNumberExpr{})}, "x:1: malformed number ''"},
		{"goto under Lua51", DialectLua51, []ast.Stmt{at(&ast.GotoStmt{Label: "l"}, 2)}, "x:2: goto needs DialectLuaJIT"},
		{"label under Lua51", DialectLua51, []ast.Stmt{&ast.LabelStmt{Name: "l"}}, "x:1: labels need DialectLuaJIT"},
		{"goto", DialectLuaJIT, []ast.Stmt{&ast.GotoStmt{Label: "l"}, &ast.LabelStmt{Name: "l"}}, ""},
		{"undefined label", DialectLuaJIT, []ast.Stmt{at(&ast.GotoStmt{Label: "l"}, 5)}, "x:5: undefined label 'l'"},
		{"duplicate label", DialectLuaJIT, []ast.Stmt{&ast.LabelStmt{Name: "l"}, at(&ast.LabelStmt{Name: "l"}, 2)}, "x:2: duplicate label 'l'"},
		{"goto into a local's scope", DialectLuaJIT, []ast.Stmt{
			at(&ast.GotoStmt{Label: "l"}, 1), local([]string{"a"}), local([]string{"b"}), &ast.LabelStmt{Name: "l"}, callStmt(ident("f")),
		}, "x:1: <goto l> jumps into the scope of local 'a'"},
		{"goto from a block into a local's scope", DialectLuaJIT, []ast.Stmt{
			&ast.DoBlockStmt{Stmts: []ast.Stmt{at(&ast.GotoStmt{Label: "l"}, 3)}}, local([]string{"a"}), &ast.LabelStmt{Name: "l"}, callStmt(ident("f")),
		}, "x:3: <goto l> jumps into the scope of local 'a'"},
		{"vararg outside a vararg function", DialectLua51, []ast.Stmt{ret(at(function(nil, false, ret(&ast.Comma3Expr{})), 6))},
			"x:6: cannot use '...' outside a vararg function"},
		{"break outside a loop", DialectLua51, []ast.Stmt{at(&ast.BreakStmt{}, 8)}, "x:8: no loop to break"},
		{"break in a function in a loop", DialectLua51, []ast.Stmt{&ast.WhileStmt{Condition: &ast.TrueExpr{}, Stmts: []ast.Stmt{
			ret(function(nil, false, &ast.BreakStmt{})),
		}}}, "x:1: no loop to break"},
		{"break in a loop", DialectLua51, []ast.Stmt{&ast.RepeatStmt{Condition: &ast.TrueExpr{}, Stmts: []ast.Stmt{&ast.BreakStmt{}}}}, ""},
		{"200 locals", DialectLua51, manyLocals(200), ""},
		{"201 locals", DialectLua51, manyLocals(201), "x:1: main function has more than 200 local variables"},
		{"201 parameters", DialectLuaJIT, []ast.Stmt{ret(at(function(make([]string, 201), false), 3))}, "x:3: function at line 3 has more than 200 local variables"},
		{"60 upvalues", DialectLua51, manyUpvalues(60), ""},
		{"61 upvalues", DialectLua51, manyUpvalues(61), "x:9: function at line 9 has more than 60 upvalues"},
		{"deep nesting", DialectLua51, []ast.Stmt{ret(nested(maxCompileDepth, &ast.TrueExpr{}))}, "x:1: chunk has too many syntax levels"},
		{"cycle", DialectLua51, []ast.Stmt{cyclic}, "x:1: chunk has too many syntax levels"},
		{"negative line", DialectLua51, []ast.Stmt{at(ret(), -1)}, "x:1: invalid line -1"},
		{"negative last line", DialectLua51, []ast.Stmt{&ast.ReturnStmt{Exprs: []ast.Expr{func() ast.Expr { e := num("1"); e.SetLastLine(-2); return e }()}}},
			"x:1: invalid line -2"},
		{"nil argument", DialectLua51, []ast.Stmt{ret(call(ident("f"), make([]ast.Expr, 3)...))}, "x:1: nil argument"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := CompileAST(c.chunk, "=x", c.dialect)
			if c.want == "" {
				if err != nil {
					t.Fatalf("error %v", err)
				}
				return
			}
			var ce *CompileError
			if !errors.As(err, &ce) {
				t.Fatalf("error %v (%T), want a *CompileError", err, err)
			}
			if err.Error() != c.want {
				t.Errorf("error %q, want %q", err.Error(), c.want)
			}
		})
	}
}

// TestCompileASTLimits checks the limits of the compiler that remain for a
// built tree: registers and Lua 5.1's local variable records.
func TestCompileASTLimits(t *testing.T) {
	args := make([]ast.Expr, 300)
	for i := range args {
		args[i] = num("1")
	}
	if _, err := CompileAST([]ast.Stmt{at(ret(call(ident("f"), args...)), 2)}, "=x", DialectLua51); err == nil ||
		err.Error() != "x:2: function or expression too complex" {
		t.Errorf("300 arguments: %v", err)
	}
	// 40000 locals in blocks of 100: Lua 5.1's 32767 records
	var stmts []ast.Stmt
	for range 400 {
		stmts = append(stmts, &ast.DoBlockStmt{Stmts: manyLocals(100)})
	}
	if _, err := CompileAST(stmts, "=x", DialectLua51); err == nil || err.Error() != "x:1: too many local variables" {
		t.Errorf("40000 locals under Lua51: %v", err)
	}
	if _, err := CompileAST(stmts, "=x", DialectLuaJIT); err != nil {
		t.Errorf("40000 locals under LuaJIT: %v", err)
	}
	// a loop too long to jump over, under LuaJIT too (the parser's model
	// of LuaJIT's code generator does not run)
	body := make([]ast.Stmt, 70000)
	for i := range body {
		body[i] = &ast.AssignStmt{Lhs: []ast.Expr{ident("x")}, Rhs: []ast.Expr{num("1")}}
	}
	loop := &ast.NumberForStmt{Name: "i", Init: num("1"), Limit: num("2"), Stmts: body}
	loop.SetLastLine(3)
	if _, err := CompileAST([]ast.Stmt{loop}, "=x", DialectLuaJIT); err == nil || err.Error() != "x:3: control structure too long" {
		t.Errorf("a long loop: %v", err)
	}
	if _, err := CompileAST(nil, "=x", Dialect(7)); err == nil {
		t.Error("no error for an unknown dialect")
	}
}

// TestLoadASTError: LoadAST's errors are LoadChunk's kind.
func TestLoadASTError(t *testing.T) {
	L := NewState(Options{SkipOpenLibs: true})
	defer L.Close()
	_, err := L.LoadAST([]ast.Stmt{at(&ast.BreakStmt{}, 3)}, "@file.lua")
	var ae *ApiError
	if !errors.As(err, &ae) || ae.Type != ApiErrorSyntax || err.Error() != "file.lua:3: no loop to break" {
		t.Fatalf("error %v", err)
	}
	var ce *CompileError
	if !errors.As(ae.Cause, &ce) || ce.Line != 3 {
		t.Errorf("cause %#v", ae.Cause)
	}
}

// treeString renders a tree with its lines, for comparisons.
func treeString(v any) string {
	var b strings.Builder
	var walk func(rv reflect.Value, depth int)
	walk = func(rv reflect.Value, depth int) {
		if depth > 1000 {
			b.WriteString("...")
			return
		}
		switch rv.Kind() {
		case reflect.Interface, reflect.Pointer:
			if rv.IsNil() {
				b.WriteString("nil")
				return
			}
			if ph, ok := rv.Interface().(ast.PositionHolder); ok {
				fmt.Fprintf(&b, "@%d-%d", ph.Line(), ph.LastLine())
			}
			walk(rv.Elem(), depth+1)
		case reflect.Struct:
			b.WriteString(rv.Type().Name() + "{")
			for i := range rv.NumField() {
				if rv.Type().Field(i).IsExported() {
					b.WriteString(rv.Type().Field(i).Name + ":")
					walk(rv.Field(i), depth+1)
					b.WriteString(" ")
				}
			}
			b.WriteString("}")
		case reflect.Slice:
			b.WriteString("[")
			for i := range rv.Len() {
				walk(rv.Index(i), depth+1)
				b.WriteString(" ")
			}
			b.WriteString("]")
		default:
			fmt.Fprintf(&b, "%q", fmt.Sprint(rv.Interface()))
		}
	}
	walk(reflect.ValueOf(v), 0)
	return b.String()
}

// TestCompileASTKeepsTree: compiling leaves the tree as it is (a numeric
// for's default step, the folded operand of a unary minus, the lines it
// takes), and compiling it again gives the same function.
func TestCompileASTKeepsTree(t *testing.T) {
	src := "for i = 1, 3 do x = -(-(2 + 3)) - -i end\nlocal t = {-1, - -2}\nreturn t"
	chunk, err := parse.ParseBytes([]byte(src), "x", parse.Lua51)
	if err != nil {
		t.Fatal(err)
	}
	// and a statement without lines
	chunk = append([]ast.Stmt{callStmt(ident("f"), &ast.UnaryMinusOpExpr{Expr: num("4")})}, chunk...)
	before := treeString(chunk)
	var dumps [][]byte
	for range 2 {
		proto, err := CompileAST(chunk, "=x", DialectLua51)
		if err != nil {
			t.Fatal(err)
		}
		dump, err := DumpFunction(newLFunctionL(proto, nil, 0))
		if err != nil {
			t.Fatal(err)
		}
		dumps = append(dumps, dump)
		if after := treeString(chunk); after != before {
			t.Fatalf("the tree changed:\n%s\n%s", before, after)
		}
	}
	if !bytes.Equal(dumps[0], dumps[1]) {
		t.Error("compiled twice, different dumps")
	}
	if _, err := Compile(chunk[1:], "x"); err != nil {
		t.Fatal(err)
	}
	if after := treeString(chunk); after != before {
		t.Fatalf("Compile changed the tree:\n%s\n%s", before, after)
	}
}

// TestCompileASTVarArg: under DialectLuaJIT no vararg function has the
// implicit "arg", under DialectLua51 those without ParList.NoArg do.
func TestCompileASTVarArg(t *testing.T) {
	chunk := []ast.Stmt{ret(call(function(nil, true, ret(ident("arg"))), num("1")))}
	for _, c := range []struct {
		dialect Dialect
		want    LValueType
	}{{DialectLua51, LTTable}, {DialectLuaJIT, LTNil}} {
		L := NewState(Options{Dialect: c.dialect, SkipOpenLibs: true})
		fn, err := L.LoadAST(chunk, "=x")
		if err != nil {
			t.Fatal(err)
		}
		L.Push(fn)
		L.Call(0, 1)
		if got := L.Get(-1).Type(); got != c.want {
			t.Errorf("%v: arg is a %v, want a %v", c.dialect, got, c.want)
		}
		L.Close()
	}
	// with NoArg under Lua51: none, and "arg" is the global
	noarg := function(nil, true, ret(ident("arg")))
	noarg.ParList.NoArg = true
	L := NewState(Options{SkipOpenLibs: true})
	defer L.Close()
	fn, err := L.LoadAST([]ast.Stmt{ret(call(noarg))}, "=x")
	if err != nil {
		t.Fatal(err)
	}
	L.SetGlobal("arg", LString("global"))
	L.Push(fn)
	L.Call(0, 1)
	if got := L.Get(-1); got != LString("global") {
		t.Errorf("NoArg: arg is %v", got)
	}
}

// runLines runs a chunk loaded with LoadAST, giving it a function lines
// that returns debug.getinfo(f, "S").linedefined and lastlinedefined of
// its argument, and the error message of calling its argument; it returns
// what the chunk returns, as strings.
func runLines(t *testing.T, chunk []ast.Stmt) []string {
	t.Helper()
	L := NewState()
	defer L.Close()
	fn, err := L.LoadAST(chunk, "@c.lua")
	if err != nil {
		t.Fatal(err)
	}
	if err := L.DoString(`function lines(f)
		local i = debug.getinfo(f, "S")
		local ok, err = pcall(f)
		return i.linedefined .. "-" .. i.lastlinedefined .. " " .. tostring(err)
	end`); err != nil {
		t.Fatal(err)
	}
	L.Push(fn)
	if err := L.PCall(0, MultRet, nil); err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := 1; i <= L.GetTop(); i++ {
		out = append(out, L.ToString(i))
	}
	return out
}

func TestCompileASTLines(t *testing.T) {
	// an unpositioned tree is on line 1: f errors at line 1
	f := function(nil, false, callStmt(ident("undefined")))
	got := runLines(t, []ast.Stmt{ret(call(ident("lines"), f))})
	if want := "1-1 c.lua:1: attempt to call global 'undefined' (a nil value)"; len(got) != 1 || got[0] != want {
		t.Errorf("unpositioned: %q, want %q", got, want)
	}

	// a node takes the line of the node before it in source order; an
	// unpositioned function ends on the last line in it
	g := function(nil, false,
		at(local([]string{"x"}, num("1")), 4),
		&ast.IfStmt{Condition: ident("x"), Then: []ast.Stmt{
			at(local([]string{"y"}), 6),
			callStmt(ident("undefined")), // line 6
		}},
	)
	got = runLines(t, []ast.Stmt{at(local([]string{"z"}), 2), ret(call(ident("lines"), g))})
	if want := "2-6 c.lua:6: attempt to call global 'undefined' (a nil value)"; len(got) != 1 || got[0] != want {
		t.Errorf("partly positioned: %q, want %q", got, want)
	}

	// an inserted call in a parsed tree takes the line of the code before
	// it
	chunk, err := parse.ParseBytes([]byte("local n = 0\n\nlocal function f()\n  n = n + 1\nend\nreturn lines(f)"), "c", parse.Lua51)
	if err != nil {
		t.Fatal(err)
	}
	body := &chunk[1].(*ast.LocalAssignStmt).Exprs[0].(*ast.FunctionExpr).Stmts
	*body = append(*body, callStmt(&ast.AttrGetExpr{Object: ident("string"), Key: str("x")}))
	got = runLines(t, chunk)
	if want := "3-5 c.lua:4: attempt to call field 'x' (a nil value)"; len(got) != 1 || got[0] != want {
		t.Errorf("inserted: %q, want %q", got, want)
	}

	// a traceback of an unpositioned tree has lines
	L := NewState()
	defer L.Close()
	tb := local([]string{"s"}, call(&ast.AttrGetExpr{Object: ident("debug"), Key: str("traceback")}))
	fn, err := L.LoadAST([]ast.Stmt{
		local([]string{"r"}, call(function(nil, false, tb, ret(ident("s"))))),
		ret(ident("r")),
	}, "=t")
	if err != nil {
		t.Fatal(err)
	}
	L.Push(fn)
	L.Call(0, 1)
	if got, want := L.ToString(-1), "stack traceback:\n\tt:1: in function <t:1>\n\tt:1: in main chunk\n\t[G]: ?"; got != want {
		t.Errorf("traceback %q, want %q", got, want)
	}
}
