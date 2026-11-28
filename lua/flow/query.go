package flow

import (
	"fmt"
	"slices"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
)

// Unreachable returns the unreachable code of the function, as luacheck
// reports it (W511, W512): the first node of each unreachable stretch,
// in source order. Going through the nodes in order, a node that no path
// from the Entry reaches is reported, unless a node reported before
// reaches it; only Statement, Condition and LoopEnd nodes are reported
// (not a break nor a label, which do nothing by themselves, nor an
// Iteration, which its for's Statement node comes before).
//
// A LoopEnd in the result is a loop whose body runs at most once: no
// iteration gets to the end of the body (W512). Any other node is
// unreachable code (W511): after a return, a break, a goto, a loop that
// doesn't end ("while true do" without a break), or in the branch of a
// constant condition that is never taken.
func (g *Graph) Unreachable() []*Node {
	var out []*Node
	var covered []bool // by node index, for the nodes of unreachable blocks
	for _, n := range g.Nodes {
		if n.Block.Reachable || covered != nil && covered[n.Index] {
			continue
		}
		if !reportable(n) {
			continue
		}
		out = append(out, n)
		if covered == nil {
			covered = make([]bool, len(g.Nodes))
		}
		g.cover(covered, n)
	}
	return out
}

func reportable(n *Node) bool {
	switch n.Kind {
	case Condition, LoopEnd:
		return true
	case Statement:
		switch n.Stmt.(type) {
		case *ast.BreakStmt, *ast.LabelStmt:
			return false
		}
		return true
	}
	return false
}

// cover marks the nodes reachable from n, n included.
func (g *Graph) cover(covered []bool, n *Node) {
	seen := map[*Block]bool{}
	var stack []*Block
	for _, m := range n.Block.Nodes[n.Index-n.Block.Nodes[0].Index:] {
		covered[m.Index] = true
	}
	for _, e := range n.Block.Succs {
		if !seen[e.To] {
			seen[e.To] = true
			stack = append(stack, e.To)
		}
	}
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if b.Reachable {
			continue
		}
		for _, m := range b.Nodes {
			covered[m.Index] = true
		}
		for _, e := range b.Succs {
			if !seen[e.To] {
				seen[e.To] = true
				stack = append(stack, e.To)
			}
		}
	}
}

// AtMostOnce returns the loops (while, numeric and generic for) of the
// function whose body runs at most once, as luacheck reports them (W512):
// the LoopEnd nodes of Unreachable.
func (g *Graph) AtMostOnce() []*Node {
	var out []*Node
	for _, n := range g.Unreachable() {
		if n.Kind == LoopEnd {
			out = append(out, n)
		}
	}
	return out
}

// Returns is how a function ends: the reachable return statements and
// whether its end is reachable.
type Returns struct {
	// Values are the reachable returns with values, Bare those without,
	// in source order.
	Values, Bare []*ast.ReturnStmt
	// NoReturn are the reachable calls of Options.NoReturn.
	NoReturn []*ast.FuncCallStmt
	// FallsOff is set if the end of the body is reachable: the function
	// may return no value without a return statement.
	FallsOff bool
}

// Inconsistent reports whether the function returns values on a path and
// none on another (a bare return or the end of the body).
func (r Returns) Inconsistent() bool {
	return len(r.Values) > 0 && (len(r.Bare) > 0 || r.FallsOff)
}

// Returns returns how the function ends.
func (g *Graph) Returns() Returns {
	var r Returns
	for _, b := range g.Exit.Preds {
		if !b.Reachable {
			continue
		}
		if len(b.Nodes) == 0 {
			r.FallsOff = true
			continue
		}
		last := b.Nodes[len(b.Nodes)-1]
		switch st := last.Stmt.(type) {
		case *ast.ReturnStmt:
			if last.Kind == Statement {
				if len(st.Exprs) > 0 {
					r.Values = append(r.Values, st)
				} else {
					r.Bare = append(r.Bare, st)
				}
				continue
			}
		case *ast.FuncCallStmt:
			if last.NoReturn {
				r.NoReturn = append(r.NoReturn, st)
				continue
			}
		}
		r.FallsOff = true
	}
	return r
}

// EmptyKind says what an empty block is.
type EmptyKind uint8

const (
	// EmptyDo is the body of "do end" (W541).
	EmptyDo EmptyKind = iota
	// EmptyThen is the block of an if or elseif (W542).
	EmptyThen
	// EmptyElse is the block of an else (W542).
	EmptyElse
	// EmptyLoop is the body of a while, repeat or for loop.
	EmptyLoop
	// EmptyFunction is the body of a function.
	EmptyFunction
)

var emptyKindNames = [...]string{"do", "then", "else", "loop", "function"}

func (k EmptyKind) String() string {
	if int(k) < len(emptyKindNames) {
		return emptyKindNames[k]
	}
	return fmt.Sprintf("EmptyKind(%d)", int(k))
}

// Empty is a block without statements.
type Empty struct {
	Kind EmptyKind
	// Stmt is the statement of the block: the DoBlockStmt, the IfStmt of
	// the then block (an elseif's own) or of the else block, the loop; nil
	// for a function.
	Stmt ast.Stmt
	// Function is the function of an EmptyFunction.
	Function *ast.FunctionExpr
	// Span is the block's span: empty, at the token after the block (the
	// "end", "else", "elseif" or "until"). An else block is known only by
	// its span: a tree without spans has no EmptyElse.
	Span ast.Span
}

// UnusedRecursive returns the function values that are used only by
// functions that are themselves unused: an unused recursive function
// ("local function f() return f() end" and no other use of f) and groups
// of mutually recursive ones, as luacheck finds them (W211, or W311 for a
// variable with other values). A function is used if the main chunk leads
// to it: a function uses another if a use in it may see the other as the
// value of a local (Def.Value), or if the other, assigned to no local, is
// in it. The values that no use sees (Used is false) are not in the
// result: they are plainly unused. In the order of Defs.
func (p *Program) UnusedRecursive() []*Def {
	n := len(p.Graphs)
	value := make([]*Def, n) // by graph: the def whose value is its function
	found := false
	for _, d := range p.Defs {
		if !d.isFunc() || p.luacheckParens && parenFunction(d, d.Value.(*ast.FunctionExpr)) {
			continue
		}
		for _, c := range d.Node.Closures {
			if c.Function == d.Value {
				value[c.ID] = d
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	fwd := make([][]*Graph, n)
	bwd := make([][]*Graph, n)
	edge := func(from, to *Graph) {
		fwd[from.ID] = append(fwd[from.ID], to)
		bwd[to.ID] = append(bwd[to.ID], from)
	}
	for _, g := range p.Graphs[1:] {
		if d := value[g.ID]; d != nil {
			for _, u := range d.users {
				edge(u, g)
			}
		} else if g.Parent != nil {
			edge(g.Parent, g)
		}
	}
	marked := make([]bool, n)
	var mark func(edges [][]*Graph, set []bool, g *Graph)
	mark = func(edges [][]*Graph, set []bool, g *Graph) {
		for _, h := range edges[g.ID] {
			if !marked[h.ID] && !set[h.ID] {
				set[h.ID] = true
				mark(edges, set, h)
			}
		}
	}
	marked[0] = true
	mark(fwd, marked, p.Graphs[0])
	// in luacheck's order: a function after those inside it
	order := make([]*Graph, 0, n)
	var post func(g *Graph)
	post = func(g *Graph) {
		for _, nd := range g.Nodes {
			for _, c := range nd.Closures {
				post(c)
			}
		}
		order = append(order, g)
	}
	post(p.Graphs[0])
	order = order[:len(order)-1] // without the main chunk
	for _, g := range order {
		if d := value[g.ID]; d != nil && !d.Used && !marked[g.ID] {
			marked[g.ID] = true
			mark(fwd, marked, g)
		}
	}
	var out []*Def
	fs, bs := make([]bool, n), make([]bool, n)
	for _, g := range order {
		if d := value[g.ID]; d == nil || !d.Used || marked[g.ID] {
			continue
		}
		clear(fs)
		clear(bs)
		mark(fwd, fs, g)
		mark(bwd, bs, g)
		for id := range fs {
			if fs[id] && bs[id] {
				marked[id] = true
				if d := value[id]; d != nil {
					out = append(out, d)
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b *Def) int { return a.ID - b.ID })
	return out
}
