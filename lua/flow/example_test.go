package flow_test

import (
	"fmt"
	"sort"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/flow"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

// A lint pass over a Program: unreachable code, loops that run at most
// once, empty blocks, functions that return values on some paths only,
// values never used or overwritten before use, variables used
// uninitialized; with luacheck's codes where it has one.
func Example() {
	src := `local function find(t, x)
  for i, v in ipairs(t) do
    if v == x then return i end
  end
end

local function first(t)
  for _, v in ipairs(t) do
    return v
  end
  return nil
end

local function main(n)
  local total = 0
  total = n
  local label
  if n > 0 then
  else
    label = "small"
  end
  local unset
  print(label, total, unset, find, first)
  while true do
    n = n - 1
  end
  print("done")
end

return main
`
	chunk, err := parse.ParseChunk([]byte(src), "example", parse.Options{Dialect: parse.LuaJIT})
	if err != nil {
		fmt.Println(err)
		return
	}
	info, err := scope.Resolve(chunk, scope.LuaJIT)
	if err != nil {
		fmt.Println(err)
		return
	}
	p := flow.Analyze(info, nil)

	type finding struct {
		offset int
		text   string
	}
	var found []finding
	report := func(offset int, format string, args ...any) {
		found = append(found, finding{offset, fmt.Sprintf("%s: %s", chunk.Pos(offset), fmt.Sprintf(format, args...))})
	}
	for _, g := range p.Graphs {
		for _, n := range g.Unreachable() {
			if n.Kind == flow.LoopEnd {
				report(n.Stmt.Span().Start, "W512 loop is executed at most once")
			} else {
				report(n.Span().Start, "W511 unreachable code")
			}
		}
		if g.Function != nil && g.Returns().Inconsistent() {
			report(g.Function.Span().Start, "function returns a value on some paths only")
		}
	}
	for _, e := range p.Empty {
		switch e.Kind {
		case flow.EmptyDo:
			report(e.Stmt.Span().Start, "W541 empty do..end block")
		case flow.EmptyThen, flow.EmptyElse:
			report(e.Stmt.Span().Start, "W542 empty %s branch", e.Kind)
		}
	}
	for _, d := range p.Defs {
		if d.Used || d.Mutated || d.Empty || d.Var.Name == "_" || len(p.VarDefs(d.Var)) < 2 {
			continue // a variable with one value is unused, not its value
		}
		if o := d.Overwritten; o != nil {
			report(d.Span().Start, "W311 value of %s is overwritten at %s before use", d.Var.Name, chunk.Pos(o.Span().Start))
		} else {
			report(d.Span().Start, "W311 value of %s is unused", d.Var.Name)
		}
	}
	for _, u := range p.Uses {
		if u.Uninitialized() {
			report(u.Ident.Span().Start, "W321 %s is used uninitialized", u.Var.Name)
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].offset < found[j].offset })
	for _, f := range found {
		fmt.Println(f.text)
	}
	// Output:
	// 1:20: function returns a value on some paths only
	// 8:3: W512 loop is executed at most once
	// 15:9: W311 value of total is overwritten at 16:3 before use
	// 18:3: W542 empty then branch
	// 23:23: W321 unset is used uninitialized
	// 27:3: W511 unreachable code
}

// The graph of a function: its blocks, their nodes and edges.
func ExampleGraph() {
	src := `local n = 0
while n < 3 do
  if n == 1 then break end
  n = n + 1
end
return n
`
	chunk, _ := parse.ParseChunk([]byte(src), "example", parse.Options{Dialect: parse.Lua51})
	info, _ := scope.Resolve(chunk, scope.Lua51)
	g := flow.Analyze(info, nil).Main()
	for _, b := range g.Blocks {
		fmt.Printf("block %d:", b.ID)
		for _, n := range b.Nodes {
			fmt.Printf(" [%s %s]", n.Kind, text(chunk, n))
		}
		for _, e := range b.Succs {
			fmt.Printf(" -%s-> %d", e.Kind, e.To.ID)
		}
		fmt.Println()
	}
	// Output:
	// block 0: [enter ] -next-> 2
	// block 1:
	// block 2: [statement local n = 0] [statement while] -next-> 3
	// block 3: [condition n < 3] -true-> 4 -false-> 5
	// block 4: [statement if] [condition n == 1] -true-> 7 -false-> 6
	// block 5: [statement return n] -next-> 1
	// block 6: [statement n = n + 1] [loop end while] -next-> 3
	// block 7: [statement break] -next-> 5
}

// text is the source of a node, the first word of a compound statement.
func text(chunk *ast.Chunk, n *flow.Node) string {
	src := chunk.Lines.Source()
	sp := n.Span()
	switch n.Stmt.(type) {
	case *ast.WhileStmt, *ast.IfStmt:
		if n.Kind != flow.Condition {
			end := sp.Start
			for end < sp.End && src[end] != ' ' {
				end++
			}
			sp.End = end
		}
	}
	return string(src[sp.Start:sp.End])
}
