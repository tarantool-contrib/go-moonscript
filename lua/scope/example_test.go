package scope_test

import (
	"fmt"
	"sort"

	"github.com/tarantool-contrib/go-moonscript/lua/parse"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

// lint lists what a lint pass over the result would report: locals that are
// never read, global writes and shadowed variables. It is the use of the
// API, not a lint framework.
func lint(src string) ([]string, error) {
	chunk, err := parse.ParseChunk([]byte(src), "example", parse.Options{Dialect: parse.LuaJIT})
	if err != nil {
		return nil, err
	}
	info, err := scope.Resolve(chunk, scope.LuaJIT)
	if err != nil {
		return nil, err
	}
	type finding struct {
		offset int
		text   string
	}
	var found []finding
	add := func(offset int, format string, args ...any) {
		found = append(found, finding{offset, fmt.Sprintf("%s: %s", chunk.Pos(offset), fmt.Sprintf(format, args...))})
	}
	for _, v := range info.Vars {
		if v.Implicit() || v.Name == "_" {
			continue
		}
		switch {
		case v.Kind == scope.KindLabel:
		case v.Kind == scope.KindParam || v.Kind == scope.KindForNum || v.Kind == scope.KindForIn:
		case len(v.Reads) == 0 && len(v.Writes) == 0:
			add(v.NameSpan.Start, "unused %s %s", v.Kind, v.Name)
		case len(v.Reads) == 0:
			add(v.NameSpan.Start, "%s %s is assigned but never read", v.Kind, v.Name)
		}
		if s := v.Shadows; s != nil {
			add(v.NameSpan.Start, "%s %s shadows the one at %s", v.Kind, v.Name, chunk.Pos(s.NameSpan.Start))
		}
	}
	for _, g := range info.Globals {
		for _, w := range g.Writes {
			add(w.Span().Start, "global write %s", g.Name)
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].offset < found[j].offset })
	out := make([]string, len(found))
	for i, f := range found {
		out[i] = f.text
	}
	return out, nil
}

// A lint pass over the result of Resolve: unused locals, global writes
// and shadowing, each at its line and column.
func Example() {
	src := `local used, unused = 1, 2
local function helper() end
local written = 0
written = used
function setup(used)
  counter = used
  return print(undefined_name)
end
`
	report, err := lint(src)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, line := range report {
		fmt.Println(line)
	}
	// Output:
	// 1:13: unused local unused
	// 2:16: unused local function helper
	// 3:7: local written is assigned but never read
	// 5:10: global write setup
	// 5:16: parameter used shadows the one at 1:7
	// 6:3: global write counter
}

// Names are found by their node (RefOf) or by an offset of the source
// (RefAt, VarAt).
func ExampleInfo_RefAt() {
	src := "local n = 1\nreturn n + m"
	chunk, _ := parse.ParseChunk([]byte(src), "example", parse.Options{Dialect: parse.Lua51})
	info, _ := scope.Resolve(chunk, scope.Lua51)
	for _, off := range []int{19, len(src) - 1} { // the n and the m
		r := info.RefAt(off)
		if r.Var != nil {
			fmt.Printf("%s at %s: %s declared at %s\n", r.Name(), chunk.Pos(r.Span().Start), r.Var.Kind, chunk.Pos(r.Var.NameSpan.Start))
		} else {
			fmt.Printf("%s at %s: global %s\n", r.Name(), chunk.Pos(r.Span().Start), r.Kind)
		}
	}
	// Output:
	// n at 2:8: local declared at 1:7
	// m at 2:12: global read
}

// A tree built or transformed by other code resolves too, keyed by its
// nodes rather than by spans.
func ExampleResolveStmts() {
	chunk, _ := parse.ParseChunk([]byte("local a = 1; return a + b"), "example", parse.Options{Dialect: parse.Lua51})
	info, _ := scope.ResolveStmts(chunk.Stmts, scope.Lua51) // as a transformer holding statements does
	for _, g := range info.Globals {
		fmt.Println("global:", g.Name, len(g.Reads), "read")
	}
	for _, v := range info.Vars {
		fmt.Println("local:", v.Name, len(v.Reads), "read")
	}
	// Output:
	// global: b 1 read
	// local: a 1 read
}
