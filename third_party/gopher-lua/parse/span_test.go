package parse

// go-moonscript patch: tests of the spans, positions, parentheses and
// comments of the tree (ast/doc.go), and of the walker.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
)

var bothDialects = []Dialect{Lua51, LuaJIT}

// describe lists the elements of a chunk in source order as
// "<type> <start>-<end> <text>", "(<count>)" after a parenthesized
// expression's.
func describe(src string, c *ast.Chunk) []string {
	var out []string
	ast.Inspect(c, func(n ast.Element) bool {
		if _, ok := n.(*ast.Chunk); ok {
			return true
		}
		s, e := c.Positions(n)
		d := fmt.Sprintf("%s %v-%v %s", strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast."), s, e,
			src[n.Span().Start:n.Span().End])
		if x, ok := n.(ast.Expr); ok && x.Parens().Count > 0 {
			ps, pe := c.Lines.Positions(x.Parens().Span)
			d += fmt.Sprintf(" (%d %v-%v)", x.Parens().Count, ps, pe)
		}
		out = append(out, d)
		return true
	})
	return out
}

func mustChunk(t *testing.T, src string, opts Options) *ast.Chunk {
	t.Helper()
	c, err := ParseChunk([]byte(src), "test", opts)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	checkSpans(t, "test", []byte(src), opts, c, true)
	return c
}

// TestSpanPositions checks the positions of the elements of chunks with
// the line breaks the lexer accepts, tabs, multi-byte identifiers, tokens
// over several lines and headers.
func TestSpanPositions(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		shebang bool
		src     string
		want    []string
		header  string
	}{
		{"crlf", Lua51, false, "a = 1\r\nb = 2\r\n", []string{
			"AssignStmt 1:1-1:6 a = 1", "IdentExpr 1:1-1:2 a", "NumberExpr 1:5-1:6 1",
			"AssignStmt 2:1-2:6 b = 2", "IdentExpr 2:1-2:2 b", "NumberExpr 2:5-2:6 2"}, ""},
		{"cr lfcr", Lua51, false, "a = 1\rb = 2\n\rc = 3\n\nd = 4\r\r e = 5", []string{
			"AssignStmt 1:1-1:6 a = 1", "IdentExpr 1:1-1:2 a", "NumberExpr 1:5-1:6 1",
			"AssignStmt 2:1-2:6 b = 2", "IdentExpr 2:1-2:2 b", "NumberExpr 2:5-2:6 2",
			"AssignStmt 3:1-3:6 c = 3", "IdentExpr 3:1-3:2 c", "NumberExpr 3:5-3:6 3",
			"AssignStmt 5:1-5:6 d = 4", "IdentExpr 5:1-5:2 d", "NumberExpr 5:5-5:6 4",
			"AssignStmt 7:2-7:7 e = 5", "IdentExpr 7:2-7:3 e", "NumberExpr 7:6-7:7 5"}, ""},
		{"tabs", Lua51, false, "\t\tx\t=\t1", []string{
			"AssignStmt 1:3-1:8 x\t=\t1", "IdentExpr 1:3-1:4 x", "NumberExpr 1:7-1:8 1"}, ""},
		{"long string", Lua51, false, "x = [[\nab\r\ncd]] y = 1", []string{
			"AssignStmt 1:1-3:5 x = [[\nab\r\ncd]]", "IdentExpr 1:1-1:2 x", "StringExpr 1:5-3:5 [[\nab\r\ncd]]",
			"AssignStmt 3:6-3:11 y = 1", "IdentExpr 3:6-3:7 y", "NumberExpr 3:10-3:11 1"}, ""},
		{"long comment", LuaJIT, false, "--[==[\r\n x \n]==] z = 1", []string{
			"AssignStmt 3:6-3:11 z = 1", "IdentExpr 3:6-3:7 z", "NumberExpr 3:10-3:11 1"}, ""},
		{"escaped line break", Lua51, false, "s = 'a\\\r\nb' t = 1", []string{
			"AssignStmt 1:1-2:3 s = 'a\\\r\nb'", "IdentExpr 1:1-1:2 s", "StringExpr 1:5-2:3 'a\\\r\nb'",
			"AssignStmt 2:4-2:9 t = 1", "IdentExpr 2:4-2:5 t", "NumberExpr 2:8-2:9 1"}, ""},
		{"\\z", LuaJIT, false, "s = 'a\\z\n \t b' t = 1", []string{
			"AssignStmt 1:1-2:6 s = 'a\\z\n \t b'", "IdentExpr 1:1-1:2 s", "StringExpr 1:5-2:6 'a\\z\n \t b'",
			"AssignStmt 2:7-2:12 t = 1", "IdentExpr 2:7-2:8 t", "NumberExpr 2:11-2:12 1"}, ""},
		{"utf-8 names", LuaJIT, false, "\tлок = 'ü' .. ключ", []string{
			"AssignStmt 1:2-1:27 лок = 'ü' .. ключ", "IdentExpr 1:2-1:8 лок",
			"StringConcatOpExpr 1:11-1:27 'ü' .. ключ", "StringExpr 1:11-1:15 'ü'", "IdentExpr 1:19-1:27 ключ"}, ""},
		{"shebang", LuaJIT, false, "#!/usr/bin/lua\nx = 1", []string{
			"AssignStmt 2:1-2:6 x = 1", "IdentExpr 2:1-2:2 x", "NumberExpr 2:5-2:6 1"}, "#!/usr/bin/lua"},
		{"bom shebang", LuaJIT, false, "\xef\xbb\xbf#!lua\r\nx = 1", []string{
			"AssignStmt 2:1-2:6 x = 1", "IdentExpr 2:1-2:2 x", "NumberExpr 2:5-2:6 1"}, "\xef\xbb\xbf#!lua"},
		{"bom", LuaJIT, false, "\xef\xbb\xbfx = 1", []string{
			"AssignStmt 1:4-1:9 x = 1", "IdentExpr 1:4-1:5 x", "NumberExpr 1:8-1:9 1"}, "\xef\xbb\xbf"},
		{"shebang only", LuaJIT, false, "# x", nil, "# x"},
		{"lua51 shebang", Lua51, true, "#!/x\r\nx = 1", []string{
			"AssignStmt 2:1-2:6 x = 1", "IdentExpr 2:1-2:2 x", "NumberExpr 2:5-2:6 1"}, "#!/x"},
		{"lua51 no shebang", Lua51, true, "x = 1", []string{
			"AssignStmt 1:1-1:6 x = 1", "IdentExpr 1:1-1:2 x", "NumberExpr 1:5-1:6 1"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := mustChunk(t, tt.src, Options{Dialect: tt.dialect, Shebang: tt.shebang, Comments: true})
			if got := describe(tt.src, c); strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
			if h := tt.src[c.Header.Start:c.Header.End]; h != tt.header {
				t.Errorf("header %q, want %q", h, tt.header)
			}
		})
	}
	if _, err := ParseChunk([]byte("#!x\nx = 1"), "test", Options{Dialect: Lua51}); err == nil {
		t.Error("a # line parses under Lua51 without Shebang")
	}
}

// TestSpanNodes checks the spans of the elements of every kind.
func TestSpanNodes(t *testing.T) {
	src := `local a, b = (f()), {x = 1, [2] = 'y'; 3}
function a.b:c(d, ...) return ((d)), (...) end
if a then elseif b then x = 1 else y = 2 end
for i = 1, 2 do end for k, v in pairs(t) do local function g() end end
while (a).b do t:m"s" break end repeat until -x * #y
do ::l:: goto l end return not a`
	want := []string{
		"LocalAssignStmt 1:1-1:42 local a, b = (f()), {x = 1, [2] = 'y'; 3}",
		"FuncCallExpr 1:15-1:18 f() (1 1:14-1:19)",
		"IdentExpr 1:15-1:16 f",
		"TableExpr 1:21-1:42 {x = 1, [2] = 'y'; 3}",
		"Field 1:22-1:27 x = 1", "StringExpr 1:22-1:23 x", "NumberExpr 1:26-1:27 1",
		"Field 1:29-1:38 [2] = 'y'", "NumberExpr 1:30-1:31 2", "StringExpr 1:35-1:38 'y'",
		"Field 1:40-1:41 3", "NumberExpr 1:40-1:41 3",
		"FuncDefStmt 2:1-2:47 function a.b:c(d, ...) return ((d)), (...) end",
		"FuncName 2:10-2:15 a.b:c", "AttrGetExpr 2:10-2:13 a.b", "IdentExpr 2:10-2:11 a", "StringExpr 2:12-2:13 b",
		"FunctionExpr 2:15-2:47 (d, ...) return ((d)), (...) end",
		"ParList 2:15-2:23 (d, ...)",
		"ReturnStmt 2:24-2:43 return ((d)), (...)",
		"IdentExpr 2:33-2:34 d (2 2:31-2:36)",
		"Comma3Expr 2:39-2:42 ... (1 2:38-2:43)",
		"IfStmt 3:1-3:45 if a then elseif b then x = 1 else y = 2 end",
		"IdentExpr 3:4-3:5 a",
		"IfStmt 3:11-3:41 elseif b then x = 1 else y = 2",
		"IdentExpr 3:18-3:19 b",
		"AssignStmt 3:25-3:30 x = 1", "IdentExpr 3:25-3:26 x", "NumberExpr 3:29-3:30 1",
		"AssignStmt 3:36-3:41 y = 2", "IdentExpr 3:36-3:37 y", "NumberExpr 3:40-3:41 2",
		"NumberForStmt 4:1-4:20 for i = 1, 2 do end", "NumberExpr 4:9-4:10 1", "NumberExpr 4:12-4:13 2",
		"GenericForStmt 4:21-4:71 for k, v in pairs(t) do local function g() end end",
		"FuncCallExpr 4:33-4:41 pairs(t)", "IdentExpr 4:33-4:38 pairs", "IdentExpr 4:39-4:40 t",
		"LocalAssignStmt 4:45-4:67 local function g() end",
		"FunctionExpr 4:61-4:67 () end", "ParList 4:61-4:63 ()",
		"WhileStmt 5:1-5:32 while (a).b do t:m\"s\" break end",
		"AttrGetExpr 5:7-5:12 (a).b", "IdentExpr 5:8-5:9 a (1 5:7-5:10)", "StringExpr 5:11-5:12 b",
		"FuncCallStmt 5:16-5:22 t:m\"s\"", "FuncCallExpr 5:16-5:22 t:m\"s\"", "IdentExpr 5:16-5:17 t", "StringExpr 5:19-5:22 \"s\"",
		"BreakStmt 5:23-5:28 break",
		"RepeatStmt 5:33-5:53 repeat until -x * #y",
		"ArithmeticOpExpr 5:46-5:53 -x * #y", "UnaryMinusOpExpr 5:46-5:48 -x", "IdentExpr 5:47-5:48 x",
		"UnaryLenOpExpr 5:51-5:53 #y", "IdentExpr 5:52-5:53 y",
		"DoBlockStmt 6:1-6:20 do ::l:: goto l end",
		"LabelStmt 6:4-6:9 ::l::", "GotoStmt 6:10-6:16 goto l",
		"ReturnStmt 6:21-6:33 return not a", "UnaryNotOpExpr 6:28-6:33 not a", "IdentExpr 6:32-6:33 a",
	}
	c := mustChunk(t, src, Options{Dialect: LuaJIT})
	if got := describe(src, c); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	text := func(s ast.Span) string { return src[s.Start:s.End] }
	st := c.Stmts
	local := st[0].(*ast.LocalAssignStmt)
	if text(local.NameSpans[0]) != "a" || text(local.NameSpans[1]) != "b" {
		t.Errorf("local names %v", local.NameSpans)
	}
	def := st[1].(*ast.FuncDefStmt)
	if text(def.Name.MethodSpan) != "c" || text(def.Func.ParList.NameSpans[0]) != "d" ||
		text(def.Func.ParList.VarargSpan) != "..." || text(def.Func.StmtsSpan) != "return ((d)), (...)" {
		t.Errorf("function %v %v %v", def.Name.MethodSpan, def.Func.ParList, def.Func.StmtsSpan)
	}
	if call := local.Exprs[0].(*ast.FuncCallExpr); !call.AdjustRet {
		t.Error("(f()) without AdjustRet")
	}
	ret := def.Func.Stmts[0].(*ast.ReturnStmt)
	if v := ret.Exprs[1].(*ast.Comma3Expr); !v.AdjustRet {
		t.Error("(...) without AdjustRet")
	}
	ifs := st[2].(*ast.IfStmt)
	elseif := ifs.Else[0].(*ast.IfStmt)
	if text(ifs.ThenSpan) != "" || ifs.ThenSpan.Start != strings.Index(src, "elseif") ||
		ifs.ElseSpan != elseif.Span() || text(elseif.ThenSpan) != "x = 1" || text(elseif.ElseSpan) != "y = 2" {
		t.Errorf("if %v %v %v %v", ifs.ThenSpan, ifs.ElseSpan, elseif.ThenSpan, elseif.ElseSpan)
	}
	nfor := st[3].(*ast.NumberForStmt)
	gfor := st[4].(*ast.GenericForStmt)
	if text(nfor.NameSpan) != "i" || nfor.StmtsSpan.Start != strings.Index(src, "end for") ||
		text(gfor.NameSpans[1]) != "v" || text(gfor.StmtsSpan) != "local function g() end" {
		t.Errorf("for %v %v %v %v", nfor.NameSpan, nfor.StmtsSpan, gfor.NameSpans, gfor.StmtsSpan)
	}
	lf := gfor.Stmts[0].(*ast.LocalAssignStmt)
	if text(lf.NameSpans[0]) != "g" || !lf.IsFunction {
		t.Errorf("local function %v", lf.NameSpans)
	}
	w := st[5].(*ast.WhileStmt)
	if m := w.Stmts[0].(*ast.FuncCallStmt).Expr.(*ast.FuncCallExpr); text(m.MethodSpan()) != "m" ||
		text(w.StmtsSpan) != "t:m\"s\" break" {
		t.Errorf("while %v %v", m.MethodSpan(), w.StmtsSpan)
	}
	if r := st[6].(*ast.RepeatStmt); r.StmtsSpan.Start != r.StmtsSpan.End || src[r.StmtsSpan.Start:][:5] != "until" {
		t.Errorf("repeat %v", r.StmtsSpan)
	}
	do := st[7].(*ast.DoBlockStmt)
	if l, g := do.Stmts[0].(*ast.LabelStmt), do.Stmts[1].(*ast.GotoStmt); text(l.NameSpan) != "l" ||
		text(g.LabelSpan) != "l" || text(do.StmtsSpan) != "::l:: goto l" {
		t.Errorf("labels %v %v %v", l.NameSpan, g.LabelSpan, do.StmtsSpan)
	}
	if text(c.StmtsSpan) != src {
		t.Errorf("chunk block %v", c.StmtsSpan)
	}
	// the lines are those of the parser's tree: a parenthesized expression is
	// on the line of its "(", a unary operation on its operand's
	c = mustChunk(t, "x = (\na)\ny = -\nb", Options{Dialect: Lua51})
	a := c.Stmts[0].(*ast.AssignStmt).Rhs[0]
	neg := c.Stmts[1].(*ast.AssignStmt).Rhs[0]
	if a.Line() != 1 || c.Pos(a.Span().Start).Line != 2 || neg.Line() != 4 || c.Pos(neg.Span().Start).Line != 3 {
		t.Errorf("lines %d %v %d %v", a.Line(), c.Pos(a.Span().Start), neg.Line(), c.Pos(neg.Span().Start))
	}
}

// TestSpanComments checks the comments of a chunk.
func TestSpanComments(t *testing.T) {
	src := "-- a\n--[[ b\r\nc ]] --[==[]==]\n---x\n--[=x\n--[[\n\rd]]return 1 -- end"
	want := []ast.Comment{
		{Span: ast.Span{Start: 0, End: 4}, Text: " a"},
		{Span: ast.Span{Start: 5, End: 17}, Text: " b\nc ", Long: true},
		{Span: ast.Span{Start: 18, End: 28}, Text: "", Long: true},
		{Span: ast.Span{Start: 29, End: 33}, Text: "-x"},
		{Span: ast.Span{Start: 34, End: 39}, Text: "[=x"},
		{Span: ast.Span{Start: 40, End: 49}, Text: "d", Long: true},
		{Span: ast.Span{Start: 58, End: 64}, Text: " end"},
	}
	for _, d := range bothDialects {
		c := mustChunk(t, src, Options{Dialect: d, Comments: true})
		if fmt.Sprint(c.Comments) != fmt.Sprint(want) {
			t.Errorf("%v: got %v, want %v", d, c.Comments, want)
		}
		if p := c.Pos(c.Comments[5].Span.Start); p != (ast.Pos{Offset: 40, Line: 6, Column: 1}) {
			t.Errorf("%v: comment at %v", d, p)
		}
		c = mustChunk(t, src, Options{Dialect: d})
		if c.Comments != nil {
			t.Errorf("%v: comments %v not asked for", d, c.Comments)
		}
	}
}

// TestSpanTree: ParseChunk's tree is ParseBytes's, which has the spans.
func TestSpanTree(t *testing.T) {
	src := []byte("local t = {f(), (g())}\nreturn t[1] .. 'x', ...")
	for _, d := range bothDialects {
		stmts, err := ParseBytes(src, "test", d)
		if err != nil {
			t.Fatal(err)
		}
		c := mustChunk(t, string(src), Options{Dialect: d, Comments: true})
		if Dump(stmts) != Dump(c.Stmts) || fingerprint(&ast.Chunk{Stmts: stmts}) != fingerprint(c) {
			t.Errorf("%v: the trees differ", d)
		}
		if a, b := describe(string(src), &ast.Chunk{Stmts: stmts, Lines: ast.NewLines(src)}), describe(string(src), c); strings.Join(a, "\n") != strings.Join(b, "\n") {
			t.Errorf("%v: the spans differ:\n%s\n%s", d, a, b)
		}
	}
}

// TestLines checks Lines.
func TestLines(t *testing.T) {
	l := ast.NewLines([]byte("ab\r\n\n\rc\rd"))
	if l.Count() != 4 || l.Start(1) != 0 || l.Start(2) != 4 || l.Start(3) != 6 || l.Start(4) != 8 || l.Start(5) != -1 || l.Start(0) != -1 {
		t.Errorf("lines %d %d %d %d %d", l.Count(), l.Start(1), l.Start(2), l.Start(3), l.Start(4))
	}
	for _, tt := range []struct {
		off  int
		want ast.Pos
	}{
		{-1, ast.Pos{Offset: 0, Line: 1, Column: 1}}, {2, ast.Pos{Offset: 2, Line: 1, Column: 3}},
		{3, ast.Pos{Offset: 3, Line: 1, Column: 4}}, {4, ast.Pos{Offset: 4, Line: 2, Column: 1}},
		{5, ast.Pos{Offset: 5, Line: 2, Column: 2}}, {6, ast.Pos{Offset: 6, Line: 3, Column: 1}},
		{9, ast.Pos{Offset: 9, Line: 4, Column: 2}}, {99, ast.Pos{Offset: 9, Line: 4, Column: 2}},
	} {
		if p := l.Pos(tt.off); p != tt.want {
			t.Errorf("Pos(%d) = %+v, want %+v", tt.off, p, tt.want)
		}
	}
	if s, e := l.Positions(ast.Span{}); s.IsValid() || e.IsValid() || s.String() != "-" {
		t.Errorf("zero span at %v %v", s, e)
	}
	if l := ast.NewLines(nil); l.Count() != 1 || l.Pos(0) != (ast.Pos{Line: 1, Column: 1}) {
		t.Errorf("empty source: %d %v", l.Count(), l.Pos(0))
	}
}

// TestSetSpan checks the spans that are no spans and those past 4 GiB.
func TestSetSpan(t *testing.T) {
	var n ast.IdentExpr
	for _, s := range []ast.Span{{Start: -1, End: 2}, {Start: 3, End: 2}, {Start: 0, End: 1 << 32}} {
		n.SetSpan(ast.Span{Start: 1, End: 2})
		if n.SetSpan(s); n.Span() != (ast.Span{}) {
			t.Errorf("SetSpan(%v): %v", s, n.Span())
		}
	}
	n.SetSpan(ast.Span{Start: 1<<32 - 2, End: 1<<32 - 1})
	if n.Span() != (ast.Span{Start: 1<<32 - 2, End: 1<<32 - 1}) {
		t.Errorf("span %v", n.Span())
	}
	c := &ast.FuncCallExpr{Method: "m"}
	if c.SetMethodSpan(ast.Span{Start: 5, End: 99}); c.MethodSpan() != (ast.Span{Start: 5, End: 6}) {
		t.Errorf("method span %v", c.MethodSpan())
	}
	if c.SetMethodSpan(ast.Span{}); c.MethodSpan() != (ast.Span{}) {
		t.Errorf("method span %v", c.MethodSpan())
	}
	n.SetParens(ast.Parens{Count: 1, Span: ast.Span{Start: 0, End: 9}})
	if n.Parens().Count != 1 || n.OuterSpan() != (ast.Span{Start: 0, End: 9}) {
		t.Errorf("parens %v %v", n.Parens(), n.OuterSpan())
	}
	if n.SetParens(ast.Parens{}); n.Parens() != (ast.Parens{}) || n.OuterSpan() != n.Span() {
		t.Errorf("parens %v %v", n.Parens(), n.OuterSpan())
	}
}

type countVisitor struct {
	visits, leaves *int
	skip           func(ast.Element) bool
}

func (v countVisitor) Visit(n ast.Element) ast.Visitor {
	if n == nil {
		*v.leaves++
		return nil
	}
	*v.visits++
	if v.skip != nil && v.skip(n) {
		return nil
	}
	return v
}

// TestWalk checks the walker: source order, nil after the children, a nil
// visitor skipping them, nil children skipped.
func TestWalk(t *testing.T) {
	src := "local x = {a = f(1, ...), [g] = h:m()}; function a.b:c() return - #x end"
	c := mustChunk(t, src, Options{Dialect: LuaJIT})
	var starts []int
	ast.Inspect(c, func(n ast.Element) bool {
		starts = append(starts, outer(n).Start)
		return true
	})
	for i := 1; i < len(starts); i++ {
		if starts[i] < starts[i-1] {
			t.Errorf("not in source order: %v", starts)
		}
	}
	var visits, leaves int
	ast.Walk(countVisitor{&visits, &leaves, nil}, c)
	if visits != len(starts) || leaves != visits {
		t.Errorf("%d elements: %d visits, %d ends", len(starts), visits, leaves)
	}
	visits, leaves = 0, 0
	ast.Walk(countVisitor{&visits, &leaves, func(n ast.Element) bool {
		_, ok := n.(*ast.TableExpr)
		return ok
	}}, c)
	if visits != 14 || leaves != 13 { // all but the table's 5 children; no end for the table
		t.Errorf("skipping a table: %d visits, %d ends", visits, leaves)
	}
	// built trees: nil children and nil pointers
	var nilCall *ast.FuncCallExpr
	tree := &ast.Chunk{Stmts: []ast.Stmt{
		&ast.NumberForStmt{Init: &ast.NumberExpr{Value: "1"}, Limit: nilCall},
		&ast.FuncDefStmt{Func: &ast.FunctionExpr{}},
		&ast.ReturnStmt{Exprs: []ast.Expr{&ast.TableExpr{Fields: []*ast.Field{nil, {Value: &ast.NilExpr{}}}}}},
	}}
	var types []string
	ast.Inspect(tree, func(n ast.Element) bool {
		types = append(types, fmt.Sprintf("%T", n))
		return true
	})
	if got := strings.Join(types, " "); got != "*ast.Chunk *ast.NumberForStmt *ast.NumberExpr *ast.FuncDefStmt "+
		"*ast.FunctionExpr *ast.ReturnStmt *ast.TableExpr *ast.Field *ast.NilExpr" {
		t.Errorf("built tree: %s", got)
	}
	ast.Inspect(nil, func(ast.Element) bool { t.Error("nil visited"); return true })
}

// luaFiles returns the Lua files under dir.
func luaFiles(t testing.TB, dir string) map[string][]byte {
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".lua") {
			return err
		}
		b, err := os.ReadFile(path)
		files[path] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// checkFiles parses the files in both dialects and checks their spans.
func checkFiles(t *testing.T, files map[string][]byte, reparse bool) {
	for _, d := range bothDialects {
		var nfiles, nodes, reparsed, comments, failed int
		for name, src := range files {
			opts := Options{Dialect: d, Comments: true, Shebang: true}
			c, err := ParseChunk(src, name, opts)
			if err != nil {
				failed++
				continue
			}
			n, r := checkSpans(t, name, src, opts, c, reparse)
			nfiles++
			nodes += n
			reparsed += r
			comments += len(c.Comments)
		}
		t.Logf("%s: %d files (%d do not parse): %d elements, %d reparsed, %d comments", dialectName(d), nfiles, failed, nodes, reparsed, comments)
	}
}

// TestSpansCompat checks the spans of the compatibility test scripts.
func TestSpansCompat(t *testing.T) {
	files := luaFiles(t, filepath.Join("..", "testdata"))
	if len(files) == 0 {
		t.Fatal("no scripts")
	}
	checkFiles(t, files, true)
}

// archivesDir holds go-moonscript's archives of Lua test suites and
// projects (internal/luasuites).
var archivesDir = filepath.Join("..", "..", "..", "internal", "luasuites", "testdata", "archives")

// TestSpansArchives checks the spans of the Lua files of the archives.
func TestSpansArchives(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join(archivesDir, "*.tar.gz"))
	if len(paths) == 0 {
		t.Skip("no archives in", archivesDir)
	}
	if testing.Short() {
		t.Skip("short")
	}
	files := map[string][]byte{}
	for _, p := range paths {
		if err := readTarGz(p, files); err != nil {
			t.Fatal(err)
		}
	}
	checkFiles(t, files, true)
}

func readTarGz(path string, files map[string][]byte) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || !strings.HasSuffix(hdr.Name, ".lua") && !strings.HasSuffix(hdr.Name, ".t") {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		files[filepath.Base(path)+"/"+hdr.Name] = b
	}
}

// FuzzSpans parses random sources and checks the spans of those that
// parse (see spanChecker), and that the tree is ParseBytes's.
func FuzzSpans(f *testing.F) {
	for _, s := range []string{
		"local a, b = (f()), {x = 1, [2] = 'y'; 3} -- c",
		"--[==[\r\nlong\r\n]==]\nfunction a.b:c(d, ...) return ((d)) end",
		"if a then elseif b then x = 1 else y = 2 end",
		"#!/bin/lua\r\nfor i = 1, 2 do ::l:: goto l end",
		"\xef\xbb\xbfs = 'a\\z\n  b' .. [[\n]] .. \"\\\r\n\"",
		"x = 0x1p4 + 1e3 + 1LL + .5 - -#t\n\r--x\rreturn",
		"while (a).b do t:m\"s\" break end repeat local z until -x * #y",
	} {
		f.Add([]byte(s))
	}
	for name, src := range luaFiles(f, filepath.Join("..", "testdata", "compat")) {
		if len(src) < 2000 && !strings.Contains(name, "limits") {
			f.Add(src)
		}
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		for _, d := range bothDialects {
			opts := Options{Dialect: d, Comments: true, Shebang: true}
			c, err := ParseChunk(src, "fuzz", opts)
			stmts, err2 := ParseBytes(src, "fuzz", d)
			if (err == nil) != (err2 == nil) && !bytes.HasPrefix(src, []byte("#")) {
				t.Fatalf("%v: ParseChunk: %v, ParseBytes: %v", d, err, err2)
			}
			if err != nil {
				continue
			}
			checkSpans(t, "fuzz", src, opts, c, len(src) < 4000)
			if err2 == nil && fingerprint(&ast.Chunk{Stmts: stmts}) != fingerprint(c) {
				t.Fatalf("%v: the trees differ", d)
			}
		}
	})
}
