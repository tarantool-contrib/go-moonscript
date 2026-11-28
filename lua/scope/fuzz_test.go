package scope_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

// checkInvariants checks what holds for the Info of any parsed chunk,
// whatever the oracle says: the lists agree with each other.
func checkInvariants(chunk *ast.Chunk, info *scope.Info) error {
	if info.Root == nil || len(info.Scopes) == 0 || info.Scopes[0] != info.Root || info.Root.Parent != nil {
		return fmt.Errorf("root")
	}
	if len(info.Problems) > 0 {
		return fmt.Errorf("problems in a parsed tree: %v", info.Problems)
	}
	for i, s := range info.Scopes {
		if s.ID != i {
			return fmt.Errorf("scope %d has ID %d", i, s.ID)
		}
		if s.Parent != nil {
			if s.Parent.ID >= s.ID {
				return fmt.Errorf("scope %d before its parent", i)
			}
			if s.Span.Start < s.Parent.Span.Start || s.Span.End > s.Parent.Span.End {
				return fmt.Errorf("scope %d (%v) %v outside its parent's %v", i, s.Kind, s.Span, s.Parent.Span)
			}
			found := false
			for _, c := range s.Parent.Children {
				found = found || c == s
			}
			if !found {
				return fmt.Errorf("scope %d not in its parent's children", i)
			}
		}
		if s.IsFunction() != (s.Func == s) {
			return fmt.Errorf("scope %d Func", i)
		}
		for j, c := range s.Children {
			if c.Parent != s || j > 0 && c.Span.Start < s.Children[j-1].Span.Start {
				return fmt.Errorf("scope %d child %d", i, j)
			}
		}
		for _, u := range s.Upvalues {
			if !s.IsFunction() || u.Scope.Func == s || !u.Captured {
				return fmt.Errorf("scope %d upvalue %s", i, u.Name)
			}
		}
	}
	lastNamed := 0
	for i, v := range info.Vars {
		if v.ID != i || v.Scope == nil {
			return fmt.Errorf("variable %d", i)
		}
		if !v.Implicit() {
			if v.NameSpan.Start < lastNamed {
				return fmt.Errorf("variable %d (%s) out of order", i, v.Name)
			}
			lastNamed = v.NameSpan.Start
		}
		if v.Implicit() == (v.NameSpan != ast.Span{}) {
			return fmt.Errorf("variable %s: implicit %v, span %v", v.Name, v.Implicit(), v.NameSpan)
		}
		found := false
		for _, u := range v.Scope.Vars {
			found = found || u == v
		}
		if !found {
			return fmt.Errorf("variable %s not in its scope", v.Name)
		}
		captured := false
		for _, rs := range [][]*scope.Ref{v.Reads, v.Writes} {
			for _, r := range rs {
				if r.Var != v || r.Name() != v.Name {
					return fmt.Errorf("variable %s: a use of %s", v.Name, r.Name())
				}
				captured = captured || r.IsUpvalue()
			}
		}
		for _, r := range v.Writes {
			if r.Kind != scope.Write {
				return fmt.Errorf("variable %s: write of kind %v", v.Name, r.Kind)
			}
		}
		if captured != v.Captured {
			return fmt.Errorf("variable %s: captured %v, uses say %v", v.Name, v.Captured, captured)
		}
		// the variable is visible where it is used: its scope contains the use
		for _, r := range append(append([]*scope.Ref(nil), v.Reads...), v.Writes...) {
			if !inScope(r.Scope, v.Scope) {
				return fmt.Errorf("variable %s used outside its scope", v.Name)
			}
		}
		if v.Shadows != nil && (v.Shadows.Name != v.Name || v.Shadows.ID >= v.ID && v.Kind != scope.KindParam) {
			return fmt.Errorf("variable %s shadows %s", v.Name, v.Shadows.Name)
		}
	}
	prev := -1
	reads, writes, globalRefs := 0, 0, 0
	for i := range info.Refs {
		r := &info.Refs[i]
		if s := r.Span().Start; s < prev {
			return fmt.Errorf("occurrence %d (%s) out of order", i, r.Name())
		} else {
			prev = s
		}
		if r.Scope == nil || (r.Ident == nil) == (r.Goto == nil) {
			return fmt.Errorf("occurrence %d", i)
		}
		if r.Var == nil && r.Kind != scope.Jump {
			globalRefs++
		}
		if (r.Kind == scope.Jump) != (r.Goto != nil) {
			return fmt.Errorf("occurrence %d kind %v", i, r.Kind)
		}
		if r.Var != nil {
			if r.Kind == scope.Write {
				writes++
			} else {
				reads++
			}
		}
		if info.RefOf(r.Ident) != nil && r.Ident != nil && info.RefOf(r.Ident) != r {
			return fmt.Errorf("RefOf %d", i)
		}
	}
	nr, nw := 0, 0
	for _, v := range info.Vars {
		nr += len(v.Reads)
		nw += len(v.Writes)
	}
	if nr != reads || nw != writes {
		return fmt.Errorf("uses: %d/%d reads, %d/%d writes", nr, reads, nw, writes)
	}
	ng := 0
	seen := map[string]bool{}
	for _, g := range info.Globals {
		if seen[g.Name] || len(g.Reads)+len(g.Writes) == 0 {
			return fmt.Errorf("global %s", g.Name)
		}
		seen[g.Name] = true
		ng += len(g.Reads) + len(g.Writes)
	}
	if ng != globalRefs {
		return fmt.Errorf("globals: %d of %d", ng, globalRefs)
	}
	return nil
}

// inScope reports whether s is inner or the same as outer.
func inScope(s, outer *scope.Scope) bool {
	for ; s != nil; s = s.Parent {
		if s == outer {
			return true
		}
	}
	return false
}

// checkResolve parses and resolves src in a dialect, checks the invariants
// and the compiler oracle; it returns an error for what is wrong.
func checkResolve(src []byte, d scope.Dialect) error {
	chunk, err := parse.ParseChunk(src, "fuzz", parse.Options{Dialect: parseDialect(d)})
	if err != nil {
		return nil
	}
	info, err := scope.Resolve(chunk, d)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	if err := checkInvariants(chunk, info); err != nil {
		return err
	}
	if _, diffs := compilerOracle(src, d); len(diffs) > 0 {
		return fmt.Errorf("the compiler: %s", strings.Join(diffs, "\n"))
	}
	return nil
}

// fuzzSeeds are sources that exercise the scoping rules.
var fuzzSeeds = []string{
	"local x = x; local x = x; return x",
	"local function f() return f end local g = function() return g end",
	"repeat local a = 1 until a",
	"for i = i, i do local i = i end for k, v in k, v do return k, v end",
	"function a.b:c(p, ...) return self, arg, ... end",
	"local f; function f() end function g() end function t.x() end function f:m() end",
	"goto l; ::l:: do goto m; local a; ::m:: end ::n:: goto n",
	"local a, b, c; return function() return function() return a, b, c end end",
	"while x do if y then goto continue end ::continue:: end",
	"if a then local x elseif b then local y else local z end",
	"local t = {f = function(self) return self end, [k] = v, w}",
	"return function(...) local arg = arg return ..., arg end",
	"local a <const> = 1",
	"::a:: ::b:: do ::a:: goto b end",
	"local x; local function f(x) local x = x; return function() return x end end",
}

func addSeeds(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	for name, src := range luaFiles(f, filepath.Join("..", "testdata", "compat")) {
		if len(src) < 3000 && !strings.Contains(name, "limits") {
			f.Add(src)
		}
	}
}

// FuzzResolve resolves random sources in both dialects: it must not panic,
// must satisfy the invariants and agree with the compiler (see
// oracle_test.go).
func FuzzResolve(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, src []byte) {
		for _, d := range dialects {
			if err := checkResolve(src, d); err != nil {
				t.Fatalf("%v: %v", d, err)
			}
		}
	})
}

// TestResolveSeeds runs the fuzz target on its seeds, and the invariants on
// the archives.
func TestResolveSeeds(t *testing.T) {
	for _, s := range fuzzSeeds {
		for _, d := range dialects {
			if err := checkResolve([]byte(s), d); err != nil {
				t.Errorf("%v: %q: %v", d, s, err)
			}
		}
	}
}

// TestInvariantsArchives checks the invariants of the Info of every file of
// the archives and of the testdata.
func TestInvariantsArchives(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	files := archiveFiles(t)
	for k, v := range luaFiles(t, filepath.Join("..", "testdata")) {
		files[k] = v
	}
	n := 0
	for _, name := range sortedKeys(files) {
		for _, d := range dialects {
			chunk, err := parse.ParseChunk(files[name], name, parse.Options{Dialect: parseDialect(d)})
			if err != nil {
				continue
			}
			n++
			info, err := scope.Resolve(chunk, d)
			if err == nil {
				err = checkInvariants(chunk, info)
			}
			if err != nil {
				t.Errorf("%s (%v): %v", name, d, err)
			}
		}
	}
	t.Logf("%d trees", n)
}
