package flow_test

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/flow"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/scope"
)

// dump writes a graph as its blocks: "id: nodes -> succs", a node as its
// kind and the start of its source, "!" before an unreachable block.
func dump(chunk *ast.Chunk, g *flow.Graph) string {
	var b strings.Builder
	for _, blk := range g.Blocks {
		if !blk.Reachable {
			b.WriteByte('!')
		}
		fmt.Fprintf(&b, "%d:", blk.ID)
		for _, n := range blk.Nodes {
			fmt.Fprintf(&b, " %s", nodeText(chunk, n))
		}
		if len(blk.Succs) > 0 {
			b.WriteString(" ->")
		}
		for _, e := range blk.Succs {
			switch e.Kind {
			case flow.True:
				b.WriteString(" T")
			case flow.False:
				b.WriteString(" F")
			default:
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "%d", e.To.ID)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// nodeText is a node's kind and the first word of its source.
func nodeText(chunk *ast.Chunk, n *flow.Node) string {
	kind := map[flow.NodeKind]string{flow.Enter: "enter", flow.Statement: "s", flow.Condition: "c", flow.Iteration: "iter", flow.LoopEnd: "end"}[n.Kind]
	if n.Kind == flow.Enter {
		return kind
	}
	sp := n.Span()
	if chunk == nil || sp == (ast.Span{}) {
		return fmt.Sprintf("%s(%T)", kind, n.Stmt)
	}
	src := chunk.Lines.Source()
	end := sp.Start
	for end < sp.End && src[end] != ' ' && src[end] != '(' {
		end++
	}
	return kind + "(" + string(src[sp.Start:end]) + ")"
}

func TestGraphs(t *testing.T) {
	cases := []struct {
		name    string
		dialect []scope.Dialect
		src     string
		want    string
	}{
		{"if", dialects, "if a then x() elseif b then y() else z() end w()", `
0: enter -> 2
1:
2: s(if) c(a) -> T4 F5
3: s(w) -> 1
4: s(x) -> 3
5: c(b) -> T6 F7
6: s(y) -> 3
7: s(z) -> 3
`},
		{"else if", dialects, "if a then else if b then y() end end", `
0: enter -> 2
1:
2: s(if) c(a) -> T4 F5
3: -> 1
4: -> 3
5: s(if) c(b) -> T6 F3
6: s(y) -> 3
`},
		{"while", dialects, "while a do if b then break end x() end", `
0: enter -> 2
1:
2: s(while) -> 3
3: c(a) -> T4 F5
4: s(if) c(b) -> T7 F6
5: -> 1
6: s(x) end(while) -> 3
7: s(break) -> 5
`},
		{"repeat", dialects, "repeat local x = f() until x", `
0: enter -> 2
1:
2: s(repeat) -> 3
3: s(local) c(x) -> T4 F3
4: -> 1
`},
		{"for", dialects, "for i = 1, 3 do x() end for k, v in pairs(t) do return k end", `
0: enter -> 2
1:
2: s(for) -> 3
3: iter(for) -> T4 F5
4: s(x) end(for) -> 3
5: s(for) -> 6
6: iter(for) -> T7 F8
7: s(return) -> 1
8: -> 1
!9: end(for) -> 6
`},
		{"constants", dialects, "while true do end if false then x() end repeat until false", `
0: enter -> 2
!1:
2: s(while) -> 3
3: c(true) -> T4
4: end(while) -> 3
!5: s(if) c(false) -> F6
!6: s(repeat) -> 8
!7: s(x) -> 6
!8: c(false) -> F8
!9: -> 1
`},
		{"goto", []scope.Dialect{scope.LuaJIT}, "for i = 1, 3 do if i == 2 then goto continue end x() ::continue:: end ::top:: goto top", `
0: enter -> 2
!1:
2: s(for) -> 3
3: iter(for) -> T4 F5
4: s(if) c(i) -> T7 F6
5: -> 9
6: s(x) -> 8
7: s(goto) -> 8
8: s(::continue::) end(for) -> 3
9: s(::top::) s(goto) -> 9
`},
		{"return in do", dialects, "do return end x()", `
0: enter -> 2
1:
2: s(do) s(return) -> 1
!3: s(x) -> 1
`},
	}
	for _, c := range cases {
		for _, d := range c.dialect {
			chunk, _, p := analyze(t, c.src, d, nil)
			if got := "\n" + dump(chunk, p.Main()); got != c.want {
				t.Errorf("%s (%v): got%s\nwant%s", c.name, d, got, c.want)
			}
		}
	}
}

// at returns "line:col" of an offset.
func at(chunk *ast.Chunk, off int) string {
	p := chunk.Pos(off)
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

func TestUnreachable(t *testing.T) {
	cases := []struct {
		src  string
		jit  bool
		want string // "line:col kind" of each unreachable node, in order
	}{
		{"do return end x()", false, "1:15 statement"},
		{"while true do end x()", false, "1:19 statement"},
		{"while true do if a then break end end x()", false, ""},
		{"repeat until false x()", false, "1:20 statement"},
		{"repeat until true x()", false, ""},
		{"if false then x() end", false, "1:15 statement"},
		{"if nil then x() end", false, "1:13 statement"},
		{"if true then x() else y() end", false, "1:23 statement"},
		{"if true then elseif c then y() end", false, "1:21 condition"},
		{"if (true) then else y() end", false, "1:21 statement"},
		{"if not false then else y() end", false, ""},
		{"while 1 do end", false, ""},
		{"while {} do end x()", false, "1:17 statement"},
		{"for i = 1, 2 do return end", false, "1:1 loop end"},
		{"while c do break end", false, "1:1 loop end"},
		{"while false do end", false, "1:1 loop end"},
		{"while false do x() end", false, "1:16 statement"},
		{"while c do do return end x() end", false, "1:26 statement"},
		{"while c do if a then break else break end end", false, "1:1 loop end"},
		{"repeat return until c", false, "1:21 condition"},
		{"repeat break until c", false, "1:20 condition"},
		{"do return end local f = function() a() end", false, "1:15 statement"},
		{"goto l x() ::l:: y()", true, "1:8 statement"},
		{"do ::l:: goto l end x()", true, "1:21 statement"},
		{"goto l ::l:: x()", true, ""},
		{"for i = 1, 2 do if i then goto c end do return end ::c:: end", true, ""},
		{"for i = 1, 2 do do return end ::c:: end", true, "1:1 loop end"},
	}
	for _, c := range cases {
		ds := dialects
		if c.jit {
			ds = []scope.Dialect{scope.LuaJIT}
		}
		for _, d := range ds {
			chunk, _, p := analyze(t, c.src, d, nil)
			var got []string
			for _, n := range p.Main().Unreachable() {
				got = append(got, at(chunk, n.Span().Start)+" "+n.Kind.String())
			}
			if strings.Join(got, ", ") != c.want {
				t.Errorf("%q (%v): unreachable %q, want %q", c.src, d, strings.Join(got, ", "), c.want)
			}
			if n := len(p.Main().AtMostOnce()); n != strings.Count(c.want, "loop end") {
				t.Errorf("%q (%v): %d loops at most once", c.src, d, n)
			}
		}
	}
}

func TestReturns(t *testing.T) {
	cases := []struct {
		src          string
		noReturn     bool
		values, bare int
		aborts       int
		fallsOff     bool
		inconsistent bool
	}{
		{"local f = function() end", false, 0, 0, 0, true, false},
		{"local f = function() return 1 end", false, 1, 0, 0, false, false},
		{"local f = function(a) if a then return 1 end end", false, 1, 0, 0, true, true},
		{"local f = function(a) if a then return 1 else return end end", false, 1, 1, 0, false, true},
		{"local f = function(a) if a then return 1 end return f(a) end", false, 2, 0, 0, false, false},
		{"local f = function(a) while true do end end", false, 0, 0, 0, false, false},
		{"local f = function(a) while true do if a then return 1 end end end", false, 1, 0, 0, false, false},
		{"local f = function(a) if a then return 1 end error('x') end", false, 1, 0, 0, true, true},
		{"local f = function(a) if a then return 1 end error('x') end", true, 1, 0, 1, false, false},
		{"local f = function(a) if a then return 1 end os.exit(1) end", true, 1, 0, 1, false, false},
		{"local error = print local f = function(a) if a then return 1 end error('x') end", true, 1, 0, 0, true, true},
		{"local f = function(a) do return 1 end return end", false, 1, 0, 0, false, false},
		{"local f = function(a) for i = 1, 2 do return i end end", false, 1, 0, 0, true, true},
	}
	for _, c := range cases {
		for _, d := range dialects {
			var opts *flow.Options
			if c.noReturn {
				opts = &flow.Options{NoReturn: []string{"error", "os.exit"}}
			}
			_, _, p := analyze(t, c.src, d, opts)
			g := p.Graphs[len(p.Graphs)-1]
			r := g.Returns()
			if len(r.Values) != c.values || len(r.Bare) != c.bare || len(r.NoReturn) != c.aborts || r.FallsOff != c.fallsOff || r.Inconsistent() != c.inconsistent {
				t.Errorf("%q (%v): %d values, %d bare, %d no-return, falls off %v, inconsistent %v",
					c.src, d, len(r.Values), len(r.Bare), len(r.NoReturn), r.FallsOff, r.Inconsistent())
			}
		}
	}
	// what follows a no-return call is unreachable
	_, _, p := analyze(t, "error('x') print(1)", scope.Lua51, &flow.Options{NoReturn: []string{"error"}})
	if u := p.Main().Unreachable(); len(u) != 1 || u[0].Index != 2 {
		t.Errorf("after error(): %v", u)
	}
	_, _, p = analyze(t, "error('x') print(1)", scope.Lua51, nil)
	if u := p.Main().Unreachable(); len(u) != 0 {
		t.Errorf("after error() without the option: %v", u)
	}
}

func TestEmpty(t *testing.T) {
	src := `do end
if a then elseif b then x() else end
while a do end
repeat until a
for i = 1, 2 do end
local f = function() end
if a then x() end
`
	for _, d := range dialects {
		chunk, _, p := analyze(t, src, d, nil)
		var got []string
		for _, e := range p.Empty {
			got = append(got, fmt.Sprintf("%s %s", e.Kind, at(chunk, e.Span.Start)))
		}
		want := "do 1:4, then 2:11, else 2:34, loop 3:12, loop 4:8, loop 5:17, function 6:22"
		if strings.Join(got, ", ") != want {
			t.Errorf("%v: %s, want %s", d, strings.Join(got, ", "), want)
		}
	}
}

// varUse returns the use of a name at a line and column ("2:7").
func varUse(t *testing.T, chunk *ast.Chunk, p *flow.Program, pos string) *flow.Use {
	t.Helper()
	for _, u := range p.Uses {
		if at(chunk, u.Ident.Span().Start) == pos {
			return u
		}
	}
	t.Fatalf("no use at %s", pos)
	return nil
}

// varDef returns the value assigned at a line and column.
func varDef(t *testing.T, chunk *ast.Chunk, p *flow.Program, pos string) *flow.Def {
	t.Helper()
	for _, d := range p.Defs {
		if at(chunk, d.Span().Start) == pos {
			return d
		}
	}
	t.Fatalf("no def at %s", pos)
	return nil
}

func defsText(chunk *ast.Chunk, defs []*flow.Def) string {
	var s []string
	for _, d := range defs {
		s = append(s, at(chunk, d.Span().Start))
	}
	return strings.Join(s, " ")
}

func TestDataFlow(t *testing.T) {
	cases := []struct {
		name string
		src  string
		opts *flow.Options
		// "use line:col sees line:col ..." or "def line:col used|mutated|
		// unused|overwritten line:col" or "use line:col uninitialized"
		checks []string
	}{
		{"branches", "local x = 1 if c then x = 2 end print(x)", nil,
			[]string{"use 1:39 sees 1:7 1:23", "def 1:7 used", "def 1:23 used"}},
		{"overwritten", "local x = 1 x = 2 print(x)", nil,
			[]string{"use 1:25 sees 1:13", "def 1:7 overwritten 1:13"}},
		{"old value", "local x = 1 x = x + 1 print(x)", nil,
			[]string{"use 1:17 sees 1:7", "use 1:29 sees 1:13"}},
		{"loop", "local x = 0 while c do x = x + 1 end print(x)", nil,
			[]string{"use 1:28 sees 1:7 1:24", "use 1:44 sees 1:7 1:24"}},
		{"unused in a loop", "local x while c do x = 1 end", nil,
			[]string{"def 1:20 unused"}},
		{"twice", "local x x, x = 1, 2 print(x)", nil,
			[]string{"def 1:9 overwritten 1:12", "use 1:27 sees 1:12"}},
		{"uninitialized", "local x print(x) x = 1", nil,
			[]string{"use 1:15 uninitialized"}},
		{"maybe initialized", "local x if c then x = 1 end print(x)", nil,
			[]string{"use 1:35 sees 1:7 1:19"}},
		{"empty extra name", "local a, b = 1 print(b)", nil,
			[]string{"use 1:22 uninitialized"}},
		{"unpacked", "local a, b = f() print(b)", nil,
			[]string{"use 1:24 sees 1:10"}},
		{"circular", "local t = {} t.x = t.y", nil,
			[]string{"def 1:7 mutated", "use 1:20 sees 1:7"}},
		{"mutated", "local t = {} t.x = 1", nil,
			[]string{"def 1:7 mutated"}},
		{"parameter", "local f = function(a) a = 1 return a end", nil,
			[]string{"def 1:20 overwritten 1:23", "use 1:36 sees 1:23"}},
		{"loop variable", "for i = 1, 2 do i = 3 print(i) end", nil,
			[]string{"def 1:5 overwritten 1:17", "use 1:29 sees 1:17"}},
		{"self", "local t = {} function t:m() self = 1 return self end", nil,
			[]string{"use 1:45 sees 1:29"}},
		// closures
		{"closure sees values before and after its creation", "local x = 1 local f = function() return x end x = 2", nil,
			[]string{"use 1:41 sees 1:7 1:47", "def 1:7 used", "def 1:47 used"}},
		{"use sees a closure's values", "local x = 1 local function set() x = 2 end set() print(x)", nil,
			[]string{"use 1:56 sees 1:7 1:34", "def 1:34 used"}},
		{"closure created after the use", "local x print(x) local function set() x = 2 end", nil,
			[]string{"use 1:15 uninitialized"}},
		{"closure after the use in a return", "local d return d, function() d = 1 end", nil,
			[]string{"use 1:16 uninitialized"}},
		{"closure before the use in a return", "local d return function() d = 1 end, d", nil,
			[]string{"use 1:38 sees 1:7 1:27"}},
		{"closures", "local x local function a() x = 1 end local function b() return x end", nil,
			[]string{"use 1:64 sees 1:7 1:28"}},
		{"closures in exclusive branches", "local function f(c) local x if c then g = function() x = 1 end else h = function() return x end end end", nil,
			[]string{"use 1:91 uninitialized", "def 1:54 unused"}},
		{"closures in exclusive branches, luacheck", "local function f(c) local x if c then g = function() x = 1 end else h = function() return x end end end", flow.LuacheckOptions(true, false, false, false),
			[]string{"use 1:91 sees 1:27 1:54", "def 1:54 used"}},
		{"closures in exclusive branches of the main chunk", "local x if c then g = function() x = 1 end else h = function() return x end end", flow.LuacheckOptions(true, false, false, false),
			[]string{"use 1:71 uninitialized"}},
		{"a declaration's closure sees its instance only", "::l:: local function f() return f end do return end f = nil goto l", nil,
			[]string{"def 1:53 unused", "use 1:33 sees 1:22"}},
		{"recursive", "local function f() return f end", nil,
			[]string{"def 1:16 used"}},
	}
	for _, c := range cases {
		chunk, _, p := analyze(t, c.src, scope.LuaJIT, c.opts)
		for _, check := range c.checks {
			f := strings.Fields(check)
			var got string
			switch f[0] {
			case "use":
				u := varUse(t, chunk, p, f[1])
				if f[2] == "uninitialized" {
					got = fmt.Sprintf("use %s uninitialized", f[1])
					if !u.Uninitialized() {
						got = fmt.Sprintf("use %s sees %s", f[1], defsText(chunk, u.Defs))
					}
				} else {
					got = fmt.Sprintf("use %s sees %s", f[1], defsText(chunk, u.Defs))
				}
			case "def":
				d := varDef(t, chunk, p, f[1])
				switch {
				case d.Used:
					got = "used"
				case d.Mutated:
					got = "mutated"
				case d.Overwritten != nil:
					got = "overwritten " + at(chunk, d.Overwritten.Span().Start)
				default:
					got = "unused"
				}
				got = fmt.Sprintf("def %s %s", f[1], got)
			}
			if got != check {
				t.Errorf("%s: %q: got %q, want %q", c.name, c.src, got, check)
			}
		}
	}
}

func TestUnusedRecursive(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"local function f() return f() end", "1:16"},
		{"local function f() return f() end f()", ""},
		{"local a, b a = function() return b() end b = function() return a() end", "1:12 1:42"},
		{"local a, b a = function() return b() end b = function() return a() end a()", ""},
		{"local function f() local function g() return f() end return g() end", "1:35 1:16"},
		{"local function f() return f() end local function g() return f() end g()", ""},
	}
	for _, c := range cases {
		chunk, _, p := analyze(t, c.src, scope.Lua51, nil)
		if got := defsText(chunk, p.UnusedRecursive()); got != c.want {
			t.Errorf("%q: %q, want %q", c.src, got, c.want)
		}
	}
}

// clearSpans removes the positions of a tree.
func clearSpans(chunk *ast.Chunk) {
	ast.Inspect(chunk, func(n ast.Element) bool {
		n.SetSpan(ast.Span{})
		switch x := n.(type) {
		case *ast.LocalAssignStmt:
			x.NameSpans = nil
		case *ast.IfStmt:
			x.ThenSpan, x.ElseSpan = ast.Span{}, ast.Span{}
		case *ast.ParList:
			x.NameSpans = nil
		}
		if e, ok := n.(ast.Expr); ok {
			e.SetParens(ast.Parens{})
		}
		return true
	})
}

// A tree without positions, from other code, has the same graphs and data
// flow, without spans.
func TestNoSpans(t *testing.T) {
	for i := range 200 {
		src := genProgram(uint64(i), true)
		chunk, err := parse.ParseChunk([]byte(src), "gen", parse.Options{Dialect: parse.LuaJIT})
		if err != nil {
			continue
		}
		info, err := scope.Resolve(chunk, scope.LuaJIT)
		if err != nil {
			t.Fatal(err)
		}
		p := flow.Analyze(info, nil)
		clearSpans(chunk)
		info2, err := scope.ResolveStmts(chunk.Stmts, scope.LuaJIT)
		if err != nil {
			t.Fatal(err)
		}
		p2 := flow.AnalyzeStmts(chunk.Stmts, info2, nil)
		if p2.Misses() != 0 {
			t.Errorf("gen %d: %d misses", i, p2.Misses())
		}
		if len(p.Graphs) != len(p2.Graphs) || len(p.Defs) != len(p2.Defs) || len(p.Uses) != len(p2.Uses) {
			t.Fatalf("gen %d: %d/%d graphs, %d/%d defs, %d/%d uses", i, len(p.Graphs), len(p2.Graphs), len(p.Defs), len(p2.Defs), len(p.Uses), len(p2.Uses))
		}
		for j := range p.Graphs {
			// "else if" is an elseif without the source
			if a, b := dump(nil, p.Graphs[j]), dump(nil, p2.Graphs[j]); a != b && !strings.Contains(src, "else\n") {
				t.Errorf("gen %d, graph %d:\n%s\nwithout spans:\n%s", i, j, a, b)
			}
		}
		for j, d := range p.Defs {
			if e := p2.Defs[j]; d.Used != e.Used || d.Mutated != e.Mutated || (d.Overwritten == nil) != (e.Overwritten == nil) {
				t.Errorf("gen %d: def %d differs without spans", i, j)
			}
		}
	}
}

// Nil nodes are skipped, by the resolver and the builder.
func TestNilNodes(t *testing.T) {
	var nilIdent *ast.IdentExpr
	stmts := []ast.Stmt{
		nil,
		&ast.LocalAssignStmt{Names: []string{"x"}, Exprs: []ast.Expr{nil}},
		&ast.AssignStmt{Lhs: []ast.Expr{nilIdent, &ast.IdentExpr{Value: "x"}}, Rhs: []ast.Expr{nil}},
		&ast.IfStmt{Condition: nil, Then: []ast.Stmt{nil, (*ast.BreakStmt)(nil)}, Else: []ast.Stmt{(*ast.IfStmt)(nil)}},
		&ast.WhileStmt{Condition: nil},
		&ast.FuncCallStmt{Expr: &ast.FuncCallExpr{Func: nil, Args: []ast.Expr{nil, &ast.FunctionExpr{}}}},
		&ast.FuncDefStmt{Name: &ast.FuncName{Func: &ast.IdentExpr{Value: "x"}}, Func: nil},
		&ast.FuncDefStmt{Name: nil, Func: &ast.FunctionExpr{ParList: &ast.ParList{Names: []string{"a"}}}},
		&ast.ReturnStmt{Exprs: []ast.Expr{&ast.IdentExpr{Value: "x"}, nil}},
	}
	for _, d := range dialects {
		info, _ := scope.ResolveStmts(stmts, d)
		p := flow.AnalyzeStmts(stmts, info, nil)
		for _, g := range p.Graphs {
			g.Unreachable()
			g.Returns()
		}
		if len(p.Graphs) != 3 {
			t.Errorf("%v: %d graphs", d, len(p.Graphs))
		}
	}
}

// A built tree whose goto has no label: the goto leads nowhere.
func TestLabelProblems(t *testing.T) {
	stmts := []ast.Stmt{
		&ast.GotoStmt{Label: "nowhere"},
		&ast.FuncCallStmt{Expr: &ast.FuncCallExpr{Func: &ast.IdentExpr{Value: "f"}}},
	}
	info, err := scope.ResolveStmts(stmts, scope.LuaJIT)
	if err == nil {
		t.Fatal("no problem")
	}
	p := flow.AnalyzeStmts(stmts, info, nil)
	g := p.Main()
	if u := g.Unreachable(); len(u) != 1 || u[0].Stmt != stmts[1] {
		t.Errorf("unreachable %v", u)
	}
	if r := g.Returns(); r.FallsOff {
		t.Errorf("returns %+v", r)
	}
	// labels under Lua51: problems, and no edges
	info, _ = scope.ResolveStmts(stmts, scope.Lua51)
	flow.AnalyzeStmts(stmts, info, nil)
}

// A Program is safe for concurrent use.
func TestConcurrent(t *testing.T) {
	chunk, _, p := analyze(t, genProgram(7, true), scope.LuaJIT, nil)
	want := fmt.Sprint(len(p.UnusedRecursive()))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for _, g := range p.Graphs {
				g.Unreachable()
				g.Returns()
			}
			for _, u := range p.Uses {
				if p.UseOf(u.Ident) != u {
					t.Error("UseOf")
				}
			}
			if got := fmt.Sprint(len(p.UnusedRecursive())); got != want {
				t.Error("UnusedRecursive", got, want)
			}
		})
	}
	wg.Wait()
	_ = chunk
}

func TestGraphOf(t *testing.T) {
	_, info, p := analyze(t, "local function f() do local x end end", scope.Lua51, nil)
	for _, s := range info.Scopes {
		g := p.Graph(s)
		if g == nil || g.Func != s.Func {
			t.Errorf("scope %d: graph %v", s.ID, g)
		}
	}
	if p.Graph(nil) != nil || p.VarDefs(nil) != nil || p.VarUses(nil) != nil || p.UseOf(nil) != nil {
		t.Error("nil lookups")
	}
	if !slices.Equal(p.Graphs[1].Nodes[1:2], p.Graphs[1].Blocks[2].Nodes[:1]) {
		t.Error("nodes")
	}
}
