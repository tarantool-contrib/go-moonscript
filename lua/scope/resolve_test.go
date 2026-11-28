package scope_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

// wantRefs checks the occurrences of names in src, in both dialects (or
// only in the dialect given): "name:kind->what" in source order, what being
// "global" or the kind of the variable and its index among the variables of
// the name, "local#1" for the second local of that name.
func wantRefs(t *testing.T, src, want string, only ...scope.Dialect) {
	t.Helper()
	ds := dialects
	if len(only) > 0 {
		ds = only
	}
	for _, d := range ds {
		chunk, info := mustResolve(t, src, d)
		if got := refs(info, chunk); got != want {
			t.Errorf("%v: %s\n got: %s\nwant: %s", d, src, got, want)
		}
	}
}

func TestLocalSeesOuter(t *testing.T) {
	wantRefs(t, "local x = x", "x:read->global")
	wantRefs(t, "local x = 1; local x = x; return x", "x:read->local#0 x:read->local#1")
	wantRefs(t, "local a, b = 1, 2; local a, b = b, a", "b:read->local#0 a:read->local#0")
	wantRefs(t, "local a, a = 1, 2; return a", "a:read->local#1")
	wantRefs(t, "local x; do local x = x end", "x:read->local#0")
	wantRefs(t, "local x = function() return x end", "x:read->global")
}

func TestLocalFunction(t *testing.T) {
	wantRefs(t, "local function f() return f end", "f:read->local function#0")
	wantRefs(t, "local f = function() return f end", "f:read->global")
	wantRefs(t, "local f; local function f() return f end", "f:read->local function#1")
	wantRefs(t, "local function f() end; f()", "f:read->local function#0")
}

func TestRepeatUntil(t *testing.T) {
	wantRefs(t, "repeat local a = 1 until a", "a:read->local#0")
	wantRefs(t, "repeat local a = 1; local b = a until a == b", "a:read->local#0 a:read->local#0 b:read->local#0")
	wantRefs(t, "repeat local a = a until a", "a:read->global a:read->local#0")
	// the condition is not in the body's scope after the loop
	wantRefs(t, "repeat local a until true; return a", "a:read->global")
	// a closure in the condition sees the body's locals
	wantRefs(t, "repeat local a until (function() return a end)()", "a:read->local#0")
}

func TestFor(t *testing.T) {
	wantRefs(t, "for i = i, i, i do local i = i end", "i:read->global i:read->global i:read->global i:read->for variable#0")
	wantRefs(t, "for i = 1, 2 do end; return i", "i:read->global")
	wantRefs(t, "for k, v in k, v do return k, v end", "k:read->global v:read->global k:read->for variable#0 v:read->for variable#0")
	wantRefs(t, "for k, v in pairs(t) do local k = k end return k", "pairs:read->global t:read->global k:read->for variable#0 k:read->global")
	// the hidden control variables are no names, and no variables
	for _, d := range dialects {
		_, info := mustResolve(t, "for i = 1, 2 do end for k, v in next do end", d)
		var names []string
		for _, v := range info.Vars {
			names = append(names, v.Name)
		}
		if got := strings.Join(names, " "); got != "i k v" {
			t.Errorf("%v: variables %q", d, got)
		}
	}
	// each variable is a new one per iteration for closures: still one Var
	wantRefs(t, "for i = 1, 2 do f(function() return i end) end", "f:read->global i:read->for variable#0")
}

func TestMethods(t *testing.T) {
	src := "function a.b:c(p, ...) return self, p, arg end"
	wantRefs(t, src, "a:read->global self:read->self#0 p:read->parameter#0 arg:read->arg#0", scope.Lua51)
	wantRefs(t, src, "a:read->global self:read->self#0 p:read->parameter#0 arg:read->global", scope.LuaJIT)
	for _, d := range dialects {
		_, info := mustResolve(t, src, d)
		var kinds []string
		for _, v := range info.Vars {
			kinds = append(kinds, fmt.Sprintf("%s/%s/%d/%v", v.Name, v.Kind, v.Index, v.NameSpan != ast.Span{}))
		}
		want := "self/self/-1/false p/parameter/0/true"
		if d == scope.Lua51 {
			want += " arg/arg/-1/false"
		}
		if got := strings.Join(kinds, " "); got != want {
			t.Errorf("%v: variables %q, want %q", d, got, want)
		}
	}
	// not a method: no self
	wantRefs(t, "function a.b.c() return self end", "a:read->global self:read->global")
	wantRefs(t, "function a:b() return function() return self end end", "a:read->global self:read->self#0")
	// a method call's receiver is read; its name is no variable
	wantRefs(t, "local o; o:m(o)", "o:read->local#0 o:read->local#0")
	// an explicit parameter self hides the implicit one
	wantRefs(t, "function a:b(self) return self end", "a:read->global self:read->parameter#1")
}

func TestArg(t *testing.T) {
	wantRefs(t, "return arg", "arg:read->global")
	wantRefs(t, "return ...; ", "")
	wantRefs(t, "return function(...) return arg end", "arg:read->arg#0", scope.Lua51)
	wantRefs(t, "return function(...) return arg end", "arg:read->global", scope.LuaJIT)
	wantRefs(t, "return function(a, ...) return arg end", "arg:read->arg#0", scope.Lua51)
	wantRefs(t, "return function(a) return arg end", "arg:read->global")
	wantRefs(t, "return function(arg, ...) return arg end", "arg:read->arg#1", scope.Lua51)
	wantRefs(t, "return function(arg, ...) return arg end", "arg:read->parameter#0", scope.LuaJIT)
	wantRefs(t, "local arg = 1; return function(...) return arg end", "arg:read->arg#1", scope.Lua51)
	wantRefs(t, "local arg = 1; return function(...) return arg end", "arg:read->local#0", scope.LuaJIT)
	// a tree built without NoArg, resolved for LuaJIT, has no arg either
	stmts := []ast.Stmt{&ast.ReturnStmt{Exprs: []ast.Expr{&ast.FunctionExpr{
		ParList: &ast.ParList{HasVargs: true},
		Stmts:   []ast.Stmt{&ast.ReturnStmt{Exprs: []ast.Expr{&ast.IdentExpr{Value: "arg"}}}},
	}}}}
	for _, d := range dialects {
		info, err := scope.ResolveStmts(stmts, d)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Refs[0].Var != nil; got != (d == scope.Lua51) {
			t.Errorf("%v: arg resolved: %v", d, got)
		}
	}
	// ParList.NoArg turns it off under Lua51
	stmts[0].(*ast.ReturnStmt).Exprs[0].(*ast.FunctionExpr).ParList.NoArg = true
	info, _ := scope.ResolveStmts(stmts, scope.Lua51)
	if info.Refs[0].Var != nil {
		t.Error("NoArg: arg resolved")
	}
}

func TestFunctionStatements(t *testing.T) {
	wantRefs(t, "function f() end", "f:write->global")
	wantRefs(t, "local f; function f() end", "f:write->local#0")
	wantRefs(t, "function t.x() end", "t:read->global")
	wantRefs(t, "local t; function t.x.y() end", "t:read->local#0")
	wantRefs(t, "local t; function t:m() end", "t:read->local#0")
	wantRefs(t, "function f() return f end", "f:write->global f:read->global")
	wantRefs(t, "local f; function f() return f end", "f:write->local#0 f:read->local#0")
	wantRefs(t, "x = 1; x, y.z = 2, x", "x:write->global x:write->global y:read->global x:read->global")
}

func TestGlobals(t *testing.T) {
	for _, d := range dialects {
		_, info := mustResolve(t, "a = b; function c() end; print(a, a.x); a.y = z", d)
		var got []string
		for _, g := range info.Globals {
			got = append(got, fmt.Sprintf("%s r%d w%d", g.Name, len(g.Reads), len(g.Writes)))
		}
		if g := strings.Join(got, "; "); g != "a r3 w1; b r1 w0; c r0 w1; print r1 w0; z r1 w0" {
			t.Errorf("%v: globals %s", d, g)
		}
		if g := info.Global("a"); g == nil || g.Writes[0].Span().Start != 0 {
			t.Errorf("%v: Global(a) = %+v", d, g)
		}
		if info.Global("nope") != nil {
			t.Error("Global(nope)")
		}
	}
}

func TestUpvalues(t *testing.T) {
	src := `
local a, b, c
local function f()
  local d = b
  return function() return c, a, d, b end
end
return function() return a end`
	for _, d := range dialects {
		_, info := mustResolve(t, src, d)
		var got []string
		for _, s := range info.Scopes {
			if s.IsFunction() {
				var names []string
				for _, u := range s.Upvalues {
					names = append(names, u.Name)
				}
				got = append(got, strings.Join(names, ","))
			}
		}
		if g := strings.Join(got, "|"); g != "|b,c,a|c,a,d,b|a" {
			t.Errorf("%v: upvalues %q", d, g)
		}
		captured := ""
		for _, v := range info.Vars {
			if v.Captured {
				captured += v.Name
			}
		}
		if captured != "abcd" {
			t.Errorf("%v: captured %q", d, captured)
		}
	}
	// reads and writes
	for _, d := range dialects {
		_, info := mustResolve(t, "local x = 1; x = x + 1; local function f() x = 2; return x end", d)
		x := info.Vars[0]
		if len(x.Reads) != 2 || len(x.Writes) != 2 || !x.Captured {
			t.Errorf("%v: x reads %d writes %d captured %v", d, len(x.Reads), len(x.Writes), x.Captured)
		}
		if !x.Writes[1].IsUpvalue() || x.Writes[0].IsUpvalue() {
			t.Errorf("%v: upvalue writes", d)
		}
		if f := info.Vars[1]; f.Used() || f.Name != "f" {
			t.Errorf("%v: f used: %v", d, f.Used())
		}
	}
}

func TestShadowing(t *testing.T) {
	src := "local x = 1; local x = x; do local x end; function f(p) local p = p end; local y; local function g(y) end; return z"
	for _, d := range dialects {
		chunk, info := mustResolve(t, src, d)
		var got []string
		for _, v := range info.Shadowing {
			got = append(got, fmt.Sprintf("%s@%s>%s@%s", v.Name, chunk.Pos(v.NameSpan.Start), v.Shadows.Name, chunk.Pos(v.Shadows.NameSpan.Start)))
		}
		if g := strings.Join(got, " "); g != "x@1:20>x@1:7 x@1:36>x@1:20 p@1:63>p@1:54 y@1:100>y@1:80" {
			t.Errorf("%v: shadowing %s", d, g)
		}
		for i, v := range info.Shadowing {
			if v.Shadows == nil || v.Shadows.Name != v.Name {
				t.Errorf("%v: %d: %v shadows %v", d, i, v.Name, v.Shadows)
			}
		}
	}
	// shadowing a global is none; the variable of an enclosing function is
	for _, d := range dialects {
		_, info := mustResolve(t, "print = 1; local print; return function() local print end", d)
		if len(info.Shadowing) != 1 || info.Shadowing[0].Scope.Func == info.Root {
			t.Errorf("%v: %d shadowing", d, len(info.Shadowing))
		}
	}
	// a loop variable and a local of the body
	for _, d := range dialects {
		_, info := mustResolve(t, "for i = 1, 2 do local i = i end", d)
		if len(info.Shadowing) != 1 || info.Shadowing[0].Shadows.Kind != scope.KindForNum ||
			info.Shadowing[0].Scope.Kind != scope.ScopeForBody {
			t.Errorf("%v: shadowing %v", d, info.Shadowing)
		}
	}
}

func TestScopes(t *testing.T) {
	src := `local a
do local b end
while a do local c end
repeat local d until d
if a then local e elseif b then local f else local g end
for i = 1, 2 do local h end
for k, v in pairs(a) do local j end
return function(p) local q end`
	for _, d := range dialects {
		chunk, info := mustResolve(t, src, d)
		var kinds []string
		for _, s := range info.Scopes {
			var vars []string
			for _, v := range s.Vars {
				vars = append(vars, v.Name)
			}
			kinds = append(kinds, fmt.Sprintf("%s(%s)", s.Kind, strings.Join(vars, ",")))
			if s.Parent != nil && (s.Span.Start < s.Parent.Span.Start || s.Span.End > s.Parent.Span.End) {
				t.Errorf("%v: scope %v %v outside its parent %v", d, s.Kind, s.Span, s.Parent.Span)
			}
			if s.Func == nil || (s.IsFunction() && s.Func != s) || (!s.IsFunction() && s.Func != s.Parent.Func) {
				t.Errorf("%v: scope %v Func", d, s.Kind)
			}
		}
		want := "function(a) do(b) while(c) repeat(d) then(e) then(f) else(g) for(i) for body(h) for(k,v) for body(j) function(p,q)"
		if got := strings.Join(kinds, " "); got != want {
			t.Errorf("%v: scopes\n got: %s\nwant: %s", d, got, want)
		}
		if info.Root.Span != chunk.Span() || info.Root.Node != ast.Element(chunk) {
			t.Errorf("%v: root %v", d, info.Root.Span)
		}
		// ScopeAt: the innermost scope at the "c"
		off := strings.Index(src, "local c") + 6
		if s := info.ScopeAt(off); s.Kind != scope.ScopeWhile {
			t.Errorf("%v: ScopeAt %v", d, s.Kind)
		}
		// the until condition is in the repeat's scope
		off = strings.Index(src, "until d") + 6
		if s := info.ScopeAt(off); s.Kind != scope.ScopeRepeat {
			t.Errorf("%v: ScopeAt(until) %v", d, s.Kind)
		}
		if s := info.ScopeOf(info.Scopes[1].Node); s != info.Scopes[1] {
			t.Errorf("%v: ScopeOf", d)
		}
		if info.ScopeOf(chunk) != info.Root {
			t.Errorf("%v: ScopeOf(chunk)", d)
		}
	}
	// an elseif chain has no else scopes of its own
	for _, d := range dialects {
		_, info := mustResolve(t, "if a then elseif b then elseif c then else end", d)
		n := 0
		for _, s := range info.Scopes {
			if s.Kind == scope.ScopeElse {
				n++
			}
		}
		if n != 0 {
			t.Errorf("%v: %d else scopes", d, n)
		}
	}
}

func TestOccurrenceLookups(t *testing.T) {
	src := "local abc = 1\nfunction f(xyz) return abc + xyz + g end"
	for _, d := range dialects {
		chunk, info := mustResolve(t, src, d)
		off := func(s string, n int) int {
			i := -1
			for ; n > 0; n-- {
				i += strings.Index(src[i+1:], s) + 1
			}
			return i
		}
		r := info.RefAt(off("abc", 2) + 1)
		if r == nil || r.Name() != "abc" || r.Var == nil || r.Var != info.Vars[0] {
			t.Errorf("%v: RefAt(abc) = %v", d, r)
		}
		if r := info.RefAt(off("g", 1)); r == nil || !r.IsGlobal() {
			t.Errorf("%v: RefAt(g) = %v", d, r)
		}
		if info.RefAt(off("return", 1)) != nil {
			t.Errorf("%v: RefAt(return)", d)
		}
		if v := info.VarAt(off("xyz", 1) + 2); v == nil || v.Kind != scope.KindParam {
			t.Errorf("%v: VarAt(xyz) = %v", d, v)
		}
		if v := info.VarAt(off("abc", 1)); v != info.Vars[0] {
			t.Errorf("%v: VarAt(abc) = %v", d, v)
		}
		if info.VarAt(off("abc", 2)) != nil {
			t.Errorf("%v: VarAt(use) is a declaration", d)
		}
		for i := range info.Refs {
			if info.RefOf(info.Refs[i].Ident) != &info.Refs[i] {
				t.Errorf("%v: RefOf %d", d, i)
			}
			if got := info.RefAt(info.Refs[i].Span().Start); got != &info.Refs[i] {
				t.Errorf("%v: RefAt %d", d, i)
			}
		}
		if info.RefOf(&ast.IdentExpr{Value: "abc"}) != nil || info.VarOf(&ast.IdentExpr{}) != nil {
			t.Errorf("%v: RefOf a foreign node", d)
		}
		if info.VarOf(info.Refs[1].Ident) != info.Vars[0] {
			t.Errorf("%v: VarOf", d)
		}
		if chunk.Pos(info.Vars[1].NameSpan.Start).String() != "2:12" {
			t.Errorf("%v: position %v", d, chunk.Pos(info.Vars[1].NameSpan.Start))
		}
	}
}

func TestGotoLabels(t *testing.T) {
	// forward, backward, out of blocks, into no block
	wantRefs(t, "goto l; ::l::", "l:jump->label#0", scope.LuaJIT)
	wantRefs(t, "::l:: goto l", "l:jump->label#0", scope.LuaJIT)
	wantRefs(t, "do goto l end ::l::", "l:jump->label#0", scope.LuaJIT)
	wantRefs(t, "::l:: do goto l end", "l:jump->label#0", scope.LuaJIT)
	wantRefs(t, "while x do if y then goto continue end ::continue:: end", "x:read->global y:read->global continue:jump->label#0", scope.LuaJIT)
	// the innermost block with the label
	wantRefs(t, "::l:: do goto l; ::l:: end goto l", "l:jump->label#1 l:jump->label#0", scope.LuaJIT)
	wantRefs(t, "::l:: do ::l:: goto l end goto l", "l:jump->label#1 l:jump->label#0", scope.LuaJIT)
	// gotos and labels do not cross functions: a label is a block's
	wantRefs(t, "::l:: ::m:: goto m goto l", "m:jump->label#0 l:jump->label#0", scope.LuaJIT)
	// a label is no local, a local is no label
	wantRefs(t, "local l; ::l:: goto l; return l", "l:jump->label#1 l:read->local#0", scope.LuaJIT)
	// goto, a label past a local at the end of the block is fine
	wantRefs(t, "do goto done; local x; ::done:: end", "done:jump->label#0", scope.LuaJIT)
	wantRefs(t, "do goto a; local x; ::a:: ::b:: end", "a:jump->label#0", scope.LuaJIT)
	// the labels and gotos are vars and refs
	_, info := mustResolve(t, "do ::a:: goto a; goto a end", scope.LuaJIT)
	a := info.Vars[0]
	if a.Kind != scope.KindLabel || len(a.Reads) != 2 || len(a.Writes) != 0 || !a.Used() || a.Scope.Kind != scope.ScopeDo {
		t.Errorf("label %+v", a)
	}
	if info.Refs[0].Goto == nil || info.Refs[0].Ident != nil || info.Refs[0].IsGlobal() || info.Refs[0].Name() != "a" {
		t.Errorf("goto ref %+v", info.Refs[0])
	}
	if len(info.Globals) != 0 {
		t.Errorf("globals %v", info.Globals)
	}
}

// lbl and the others build statements as other code would.
func lbl(name string) ast.Stmt  { return &ast.LabelStmt{Name: name} }
func jump(name string) ast.Stmt { return &ast.GotoStmt{Label: name} }
func local(name string) ast.Stmt {
	return &ast.LocalAssignStmt{Names: []string{name}}
}
func do(stmts ...ast.Stmt) ast.Stmt { return &ast.DoBlockStmt{Stmts: stmts} }
func rep(cond string, stmts ...ast.Stmt) ast.Stmt {
	return &ast.RepeatStmt{Stmts: stmts, Condition: &ast.IdentExpr{Value: cond}}
}

func TestLabelProblems(t *testing.T) {
	cases := []struct {
		name  string
		stmts []ast.Stmt
		want  string // problems, "; "-separated
	}{
		{"undefined", []ast.Stmt{jump("l")}, "undefined label 'l'"},
		{"undefined in a block", []ast.Stmt{do(jump("l"))}, "undefined label 'l'"},
		{"label of another block", []ast.Stmt{do(lbl("l")), jump("l")}, "undefined label 'l'"},
		{"label of another function", []ast.Stmt{lbl("l"), &ast.FuncCallStmt{Expr: &ast.FuncCallExpr{
			Func: &ast.FunctionExpr{ParList: &ast.ParList{}, Stmts: []ast.Stmt{jump("l")}}}}}, "undefined label 'l'"},
		{"duplicate", []ast.Stmt{lbl("l"), lbl("l")}, "duplicate label 'l'"},
		{"duplicate in a block", []ast.Stmt{lbl("l"), do(lbl("l")), local("x"), lbl("l")}, "duplicate label 'l'"},
		{"into the scope of a local", []ast.Stmt{jump("l"), local("x"), lbl("l"), local("y")}, "<goto l> jumps into the scope of local 'x'"},
		{"from a block", []ast.Stmt{local("a"), do(jump("l")), local("x"), lbl("l"), &ast.ReturnStmt{}}, "<goto l> jumps into the scope of local 'x'"},
		{"backward over locals", []ast.Stmt{lbl("l"), local("x"), jump("l")}, ""},
		{"at the end of the block", []ast.Stmt{do(jump("l"), local("x"), lbl("l"))}, ""},
		{"at the end, before labels", []ast.Stmt{do(jump("l"), local("x"), lbl("l"), lbl("m"))}, ""},
		{"label then statement", []ast.Stmt{do(jump("l"), local("x"), lbl("l"), lbl("m"), jump("m"))}, "<goto l> jumps into the scope of local 'x'"},
		{"end of a function", []ast.Stmt{&ast.FuncCallStmt{Expr: &ast.FuncCallExpr{Func: &ast.FunctionExpr{ParList: &ast.ParList{},
			Stmts: []ast.Stmt{jump("l"), local("x"), lbl("l")}}}}}, ""},
		{"end of the main chunk", []ast.Stmt{jump("l"), local("x"), lbl("l")}, ""},
		{"end of a repeat body", []ast.Stmt{rep("c", jump("l"), local("x"), lbl("l"))}, "<goto l> jumps into the scope of local 'x'"},
		{"end of a repeat's inner block", []ast.Stmt{rep("c", do(jump("l"), local("x"), lbl("l")))}, ""},
		{"backward in a repeat", []ast.Stmt{rep("c", lbl("l"), local("x"), jump("l"))}, ""},
	}
	for _, tc := range cases {
		info, err := scope.ResolveStmts(tc.stmts, scope.LuaJIT)
		var got []string
		for _, p := range info.Problems {
			got = append(got, p.Msg)
		}
		if g := strings.Join(got, "; "); g != tc.want {
			t.Errorf("%s: problems %q, want %q", tc.name, g, tc.want)
		}
		if (err != nil) != (tc.want != "") {
			t.Errorf("%s: error %v", tc.name, err)
		}
		if err != nil && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q", tc.name, err)
		}
	}
	// Lua51 has no labels
	info, err := scope.ResolveStmts([]ast.Stmt{lbl("l"), jump("l")}, scope.Lua51)
	if err == nil || len(info.Problems) != 2 {
		t.Errorf("Lua51: %v", err)
	}
}

func TestBuiltTrees(t *testing.T) {
	// a tree without spans or lines resolves; the spans are zero
	stmts := []ast.Stmt{
		&ast.LocalAssignStmt{Names: []string{"x"}, Exprs: []ast.Expr{&ast.IdentExpr{Value: "x"}}},
		&ast.AssignStmt{Lhs: []ast.Expr{&ast.IdentExpr{Value: "x"}, &ast.IdentExpr{Value: "y"}},
			Rhs: []ast.Expr{&ast.NilExpr{}, &ast.IdentExpr{Value: "x"}}},
		&ast.FuncDefStmt{Name: &ast.FuncName{Receiver: &ast.IdentExpr{Value: "x"}, Method: "m"},
			Func: &ast.FunctionExpr{ParList: &ast.ParList{Names: []string{"p"}}, Stmts: []ast.Stmt{
				&ast.ReturnStmt{Exprs: []ast.Expr{&ast.IdentExpr{Value: "self"}, &ast.IdentExpr{Value: "p"}}}}}},
	}
	info, err := scope.ResolveStmts(stmts, scope.LuaJIT)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(len(info.Refs), len(info.Vars), len(info.Globals), info.Root.Node == nil, info.Root.Span),
		"7 3 2 true 0-0"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if info.Chunk() != nil {
		t.Error("Chunk()")
	}
	if info.RefAt(0) != nil || info.VarAt(0) != nil {
		t.Error("lookup by offset in a tree without spans")
	}
	if r := info.RefOf(stmts[0].(*ast.LocalAssignStmt).Exprs[0].(*ast.IdentExpr)); r == nil || !r.IsGlobal() {
		t.Errorf("RefOf %v", r)
	}
	// the same tree without a chunk, nil chunk
	if info, err := scope.Resolve(nil, scope.LuaJIT); err != nil || info.Root == nil || len(info.Scopes) != 1 {
		t.Errorf("Resolve(nil): %v", err)
	}
}

func TestNilNodes(t *testing.T) {
	var nilIdent *ast.IdentExpr
	var nilFunc *ast.FunctionExpr
	var nilLocal *ast.LocalAssignStmt
	stmts := []ast.Stmt{
		nil, nilLocal, (*ast.IfStmt)(nil), (*ast.GotoStmt)(nil),
		&ast.AssignStmt{Lhs: []ast.Expr{nilIdent, nil}, Rhs: []ast.Expr{nil, nilFunc}},
		&ast.FuncDefStmt{}, &ast.FuncDefStmt{Name: &ast.FuncName{}, Func: nilFunc},
		&ast.FuncDefStmt{Name: &ast.FuncName{Func: nilIdent}, Func: &ast.FunctionExpr{}},
		&ast.NumberForStmt{Name: "i"}, &ast.GenericForStmt{Names: []string{"k"}},
		&ast.RepeatStmt{Condition: nilIdent}, &ast.WhileStmt{}, &ast.ReturnStmt{Exprs: []ast.Expr{nil}},
		&ast.FuncCallStmt{Expr: &ast.FuncCallExpr{}}, &ast.FuncCallStmt{Expr: (*ast.FuncCallExpr)(nil)},
		&ast.IfStmt{Then: []ast.Stmt{nil}, Else: []ast.Stmt{(*ast.IfStmt)(nil)}},
		&ast.LocalAssignStmt{Names: []string{"a"}, Exprs: []ast.Expr{(*ast.TableExpr)(nil), &ast.TableExpr{Fields: []*ast.Field{nil, {}}}}},
	}
	for _, d := range dialects {
		if _, err := scope.ResolveStmts(stmts, d); err != nil {
			t.Errorf("%v: %v", d, err)
		}
	}
}

func TestDepth(t *testing.T) {
	// a chain deeper than the compiler goes
	var e ast.Expr = &ast.IdentExpr{Value: "a"}
	for range 300000 {
		e = &ast.AttrGetExpr{Object: e, Key: &ast.StringExpr{Value: "k"}}
	}
	info, err := scope.ResolveStmts([]ast.Stmt{&ast.ReturnStmt{Exprs: []ast.Expr{e}}}, scope.Lua51)
	if err == nil || !strings.Contains(err.Error(), "too deep") || info == nil {
		t.Errorf("deep tree: %v", err)
	}
	// a tree with a cycle
	loop := &ast.DoBlockStmt{}
	loop.Stmts = []ast.Stmt{loop, loop}
	if _, err := scope.ResolveStmts([]ast.Stmt{loop}, scope.Lua51); err == nil {
		t.Error("cyclic tree")
	}
	// a long chain that is not too deep resolves
	e = &ast.IdentExpr{Value: "a"}
	for range 50000 {
		e = &ast.AttrGetExpr{Object: e, Key: &ast.StringExpr{Value: "k"}}
	}
	if _, err := scope.ResolveStmts([]ast.Stmt{&ast.ReturnStmt{Exprs: []ast.Expr{e}}}, scope.Lua51); err != nil {
		t.Error(err)
	}
}

// TestConcurrentLookups uses the lazily built indexes of one Info from
// several goroutines (for go test -race).
func TestConcurrentLookups(t *testing.T) {
	src := "local a = 1\nfunction f(b) return a + b + c end"
	chunk, info := mustResolve(t, src, scope.LuaJIT)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range info.Refs {
				r := &info.Refs[i]
				if info.RefOf(r.Ident) != r || info.RefAt(r.Span().Start) != r {
					t.Error("lookup")
				}
			}
			if info.VarAt(info.Vars[0].NameSpan.Start) != info.Vars[0] || info.ScopeOf(chunk) != info.Root {
				t.Error("lookup")
			}
		}()
	}
	wg.Wait()
}
