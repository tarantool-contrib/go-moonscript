package flow_test

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/flow"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/scope"
)

// checkProgram checks the invariants of a Program of a parsed tree and
// returns what is wrong:
//
//   - the graphs: one per function, Entry and Exit, the blocks' nodes
//     consecutive and in one block each, the edges and their predecessors,
//     the reachable blocks those that a walk from the Entry finds;
//   - every statement in one node (an elseif in its Condition);
//   - the unreachable nodes in unreachable blocks;
//   - a use for every read of a local, a def for every write and
//     declaration, in their nodes;
//   - the values each use sees: of its variable, in order, once; a used or
//     mutated value seen by a read or a mutation; and for the uses in the
//     variable's own function, the values of that function that reach
//     them, as a walk of the nodes finds them (the reaching definitions,
//     brute force), up to maxPairs pairs.
func checkProgram(chunk *ast.Chunk, info *scope.Info, p *flow.Program, maxPairs int) []string {
	var errs []string
	fail := func(format string, args ...any) {
		if len(errs) < 20 {
			errs = append(errs, fmt.Sprintf(format, args...))
		}
	}
	if p.Misses() != 0 {
		fail("%d lookups out of the resolution's order", p.Misses())
	}
	// functions
	funcs := map[*ast.FunctionExpr]int{}
	ast.Inspect(chunk, func(n ast.Element) bool {
		if fe, ok := n.(*ast.FunctionExpr); ok {
			funcs[fe]++
		}
		return true
	})
	if len(p.Graphs) != len(funcs)+1 {
		fail("%d graphs for %d functions", len(p.Graphs), len(funcs))
	}
	stmtNodes := map[ast.Stmt][]*flow.Node{}
	for i, g := range p.Graphs {
		checkGraph(g, i, fail)
		if i > 0 && funcs[g.Function] != 1 {
			fail("graph %d: function not in the tree", i)
		}
		for _, n := range g.Nodes {
			if n.Stmt != nil {
				stmtNodes[n.Stmt] = append(stmtNodes[n.Stmt], n)
			}
		}
		for _, n := range g.Unreachable() {
			if n.Block.Reachable {
				fail("graph %d: unreachable %v in a reachable block", i, n)
			}
		}
	}
	// statements
	src := chunk.Lines.Source()
	elseifs := map[*ast.IfStmt]bool{}
	ast.Inspect(chunk, func(n ast.Element) bool {
		if st, ok := n.(*ast.IfStmt); ok && len(st.Else) == 1 {
			if ei, ok := st.Else[0].(*ast.IfStmt); ok && strings.HasPrefix(string(src[ei.Span().Start:]), "elseif") {
				elseifs[ei] = true
			}
		}
		return true
	})
	ast.Inspect(chunk, func(n ast.Element) bool {
		st, ok := n.(ast.Stmt)
		if !ok {
			return true
		}
		var heads, conds int
		for _, nd := range stmtNodes[st] {
			switch nd.Kind {
			case flow.Statement:
				heads++
			case flow.Condition:
				conds++
			}
		}
		ei, _ := st.(*ast.IfStmt)
		switch {
		case elseifs[ei]:
			if heads != 0 || conds != 1 {
				fail("elseif at %v: %d statement nodes, %d conditions", st.Span(), heads, conds)
			}
		case heads != 1:
			fail("%T at %v: %d statement nodes", st, st.Span(), heads)
		}
		return true
	})
	checkData(info, p, maxPairs, fail)
	return errs
}

func checkGraph(g *flow.Graph, id int, fail func(string, ...any)) {
	if g.ID != id || (id == 0) != (g.Function == nil) || (id == 0) != (g.Parent == nil) {
		fail("graph %d: id %d, function %v, parent %v", id, g.ID, g.Function != nil, g.Parent != nil)
	}
	if g.Parent != nil && (g.Creator == nil || g.Creator.Block.Graph != g.Parent || !slices.Contains(g.Creator.Closures, g)) {
		fail("graph %d: creator", id)
	}
	if g.Blocks[0] != g.Entry || g.Blocks[1] != g.Exit || len(g.Entry.Preds) != 0 || len(g.Exit.Succs) != 0 || len(g.Exit.Nodes) != 0 {
		fail("graph %d: entry or exit", id)
	}
	if len(g.Entry.Nodes) != 1 || g.Entry.Nodes[0].Kind != flow.Enter {
		fail("graph %d: entry nodes", id)
	}
	total := 0
	preds := map[[2]*flow.Block]int{}
	for i, b := range g.Blocks {
		if b.ID != i || b.Graph != g {
			fail("graph %d: block %d id %d", id, i, b.ID)
		}
		total += len(b.Nodes)
		for j, n := range b.Nodes {
			if n.Block != b || j > 0 && n.Index != b.Nodes[j-1].Index+1 {
				fail("graph %d: block %d nodes", id, i)
			}
			last := j == len(b.Nodes)-1
			if (n.Kind == flow.Condition || n.Kind == flow.Iteration) && !last {
				fail("graph %d: %v not last in its block", id, n)
			}
		}
		for _, e := range b.Succs {
			preds[[2]*flow.Block{b, e.To}]++
			if e.Kind != flow.Next {
				if len(b.Nodes) == 0 || k(b) != flow.Condition && k(b) != flow.Iteration {
					fail("graph %d: block %d: %s edge without a condition", id, i, e.Kind)
				}
			}
		}
		for _, p := range b.Preds {
			preds[[2]*flow.Block{p, b}]--
		}
	}
	for e, n := range preds {
		if n != 0 {
			fail("graph %d: edge %d->%d and its preds differ by %d", id, e[0].ID, e[1].ID, n)
		}
	}
	if total != len(g.Nodes) {
		fail("graph %d: %d nodes in blocks, %d in the graph", id, total, len(g.Nodes))
	}
	for i, n := range g.Nodes {
		if n.Index != i {
			fail("graph %d: node %d has index %d", id, i, n.Index)
		}
	}
	seen := map[*flow.Block]bool{g.Entry: true}
	stack := []*flow.Block{g.Entry}
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range b.Succs {
			if !seen[e.To] {
				seen[e.To] = true
				stack = append(stack, e.To)
			}
		}
	}
	for _, b := range g.Blocks {
		if b.Reachable != seen[b] {
			fail("graph %d: block %d reachable %v", id, b.ID, b.Reachable)
		}
	}
}

func k(b *flow.Block) flow.NodeKind { return b.Nodes[len(b.Nodes)-1].Kind }

func checkData(info *scope.Info, p *flow.Program, maxPairs int, fail func(string, ...any)) {
	uses := map[*ast.IdentExpr]*flow.Use{}
	for _, u := range p.Uses {
		if uses[u.Ident] != nil {
			fail("two uses of %s at %v", u.Var.Name, u.Ident.Span())
		}
		uses[u.Ident] = u
		if !slices.Contains(u.Node.Uses, u) {
			fail("use of %s at %v not in its node", u.Var.Name, u.Ident.Span())
		}
		for i, d := range u.Defs {
			if d.Var != u.Var || i > 0 && u.Defs[i-1].ID >= d.ID {
				fail("use of %s at %v: values %v", u.Var.Name, u.Ident.Span(), u.Defs)
			}
		}
	}
	names := map[*ast.IdentExpr]*flow.Def{}
	inits := map[*scope.Var]int{}
	for _, d := range p.Defs {
		if !slices.Contains(d.Node.Defs, d) {
			fail("%v not in its node", d)
		}
		if d.Name != nil {
			names[d.Name] = d
		}
		if d.Init {
			inits[d.Var]++
		}
		if d.Overwritten != nil && (d.Used || d.Mutated || d.Overwritten.Var != d.Var || d.Overwritten == d) {
			fail("%v overwritten by %v", d, d.Overwritten)
		}
	}
	for i := range info.Refs {
		r := &info.Refs[i]
		if r.Var == nil || r.Kind == scope.Jump {
			continue
		}
		switch r.Kind {
		case scope.Read:
			if u := uses[r.Ident]; u == nil || u.Var != r.Var {
				fail("read of %s at %v: no use", r.Name(), r.Span())
			}
		case scope.Write:
			if d := names[r.Ident]; d == nil || d.Var != r.Var {
				fail("write of %s at %v: no def", r.Name(), r.Span())
			}
		}
	}
	for _, v := range info.Vars {
		if v.Kind != scope.KindLabel && inits[v] != 1 {
			fail("%s at %v: %d declarations' values", v.Name, v.NameSpan, inits[v])
		}
	}
	// a used or mutated value is seen by a read or a mutation
	seenBy := map[*flow.Def][2]bool{}
	for _, u := range p.Uses {
		for _, d := range u.Defs {
			s := seenBy[d]
			s[u.Kind] = true
			seenBy[d] = s
		}
	}
	for _, d := range p.Defs {
		if s := seenBy[d]; d.Used && !s[flow.Read] || d.Mutated && !s[flow.Mutate] {
			fail("%v used %v mutated %v, seen by %v", d, d.Used, d.Mutated, s)
		}
	}
	// the reaching definitions of the main uses, brute force
	pairs := 0
	for _, u := range p.Uses {
		g := p.Graph(u.Var.Scope)
		if u.Node.Block.Graph != g {
			continue
		}
		var want []*flow.Def
		for _, d := range p.VarDefs(u.Var) {
			if d.Node.Block.Graph != g || !lastOfNode(d) {
				continue
			}
			if pairs++; pairs > maxPairs {
				return
			}
			if reachesWithout(d, u.Node) {
				want = append(want, d)
			}
		}
		var got []*flow.Def
		for _, d := range u.Defs {
			if d.Node.Block.Graph == g {
				got = append(got, d)
			}
		}
		if !slices.Equal(got, want) {
			fail("use of %s at %v: main values %v, a walk finds %v", u.Var.Name, u.Ident.Span(), got, want)
		}
	}
}

func lastOfNode(d *flow.Def) bool {
	for i := len(d.Node.Defs) - 1; i >= 0; i-- {
		if e := d.Node.Defs[i]; e.Var == d.Var {
			return e == d
		}
	}
	return false
}

// successors returns the nodes that may run right after a node.
func successors(n *flow.Node) []*flow.Node {
	b := n.Block
	if i := n.Index - b.Nodes[0].Index; i+1 < len(b.Nodes) {
		return []*flow.Node{b.Nodes[i+1]}
	}
	var out []*flow.Node
	seen := map[*flow.Block]bool{}
	var walk func(b *flow.Block)
	walk = func(b *flow.Block) {
		for _, e := range b.Succs {
			if seen[e.To] {
				continue
			}
			seen[e.To] = true
			if len(e.To.Nodes) > 0 {
				out = append(out, e.To.Nodes[0])
			} else {
				walk(e.To)
			}
		}
	}
	walk(b)
	return out
}

// reachesWithout reports whether a path leads from the node of a def to a
// node without passing another node that assigns the def's variable.
func reachesWithout(d *flow.Def, to *flow.Node) bool {
	seen := map[*flow.Node]bool{}
	stack := successors(d.Node)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		if n == to {
			return true
		}
		assigns := false
		for _, e := range n.Defs {
			if e.Var == d.Var {
				assigns = true
			}
		}
		if !assigns {
			stack = append(stack, successors(n)...)
		}
	}
	return false
}
