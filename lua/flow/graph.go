package flow

import (
	"fmt"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

// NodeKind says what a node of a graph does.
type NodeKind uint8

const (
	// Enter is the start of a function, the only node of its Entry block:
	// the parameters (and the implicit self and arg) get their values.
	Enter NodeKind = iota
	// Statement is a statement: a simple one (an assignment, a local, a
	// call, a function statement, return, break, goto, a label), or the
	// start of a compound one (do, if, while, repeat, and a for, whose
	// node evaluates the loop's expressions). An elseif has no Statement
	// node, only its Condition.
	Statement
	// Condition is the evaluation of a condition: of an if or elseif (Stmt
	// is that IfStmt), of a while, of a repeat's until. Its block ends with
	// it, a True edge to where the condition holds and a False edge to
	// where it does not.
	Condition
	// Iteration is the start of an iteration of a for loop: the test of
	// the loop's end and the values of the loop's variables. Its block ends
	// with it, a True edge into the body and a False edge out of the loop.
	Iteration
	// LoopEnd is the end of an iteration of the body of a while or a for
	// loop, from which it goes back to the loop's Condition or Iteration.
	// It is unreachable when no iteration ends: the body runs at most once.
	LoopEnd
)

var nodeKindNames = [...]string{"enter", "statement", "condition", "iteration", "loop end"}

func (k NodeKind) String() string {
	if int(k) < len(nodeKindNames) {
		return nodeKindNames[k]
	}
	return fmt.Sprintf("NodeKind(%d)", int(k))
}

// Node is a step of a function: a statement or a part of a compound one.
// The nodes of a graph are in Graph.Nodes in source order.
type Node struct {
	Kind NodeKind
	// Stmt is the statement: the statement of a Statement node, the IfStmt
	// (of the if or the elseif), WhileStmt or RepeatStmt of a Condition,
	// the loop of an Iteration or a LoopEnd; nil for Enter.
	Stmt ast.Stmt
	// Expr is the condition of a Condition node, nil for the others.
	Expr ast.Expr
	// Block is the block the node is in.
	Block *Block
	// Scope is the innermost scope the node is in (that of the loop's
	// variables for an Iteration, the one around the loop for a LoopEnd),
	// nil if the resolution didn't give it (a tree too deep).
	Scope *scope.Scope
	// Index is the index of the node in Graph.Nodes.
	Index int
	// NoReturn is set for a call statement that Options.NoReturn names:
	// its block goes to the Exit, not on.
	NoReturn bool

	// Defs are the values the node assigns to variables, in the order of
	// the names (see Def); Uses the occurrences of variables it reads or
	// mutates, in source order. Those in the functions it creates are
	// theirs, not the node's.
	Defs []*Def
	Uses []*Use
	// Closures are the functions whose expressions the node evaluates, the
	// FunctionExprs directly in its expressions, in source order.
	Closures []*Graph

	nd, nu   int32
	circular *scope.Var // the variable of a circular assignment "t.k = e"
}

// Span returns the span of what the node is in the source: the statement,
// the condition (with its parentheses), the zero Span for an Enter node or
// a node of a tree without positions.
func (n *Node) Span() ast.Span {
	switch {
	case n.Kind == Condition && n.Expr != nil:
		return n.Expr.OuterSpan()
	case n.Stmt != nil && n.Kind != Enter:
		return n.Stmt.Span()
	}
	return ast.Span{}
}

func (n *Node) String() string {
	return fmt.Sprintf("%s %T at %v", n.Kind, n.Stmt, n.Span())
}

// EdgeKind says when an edge is taken.
type EdgeKind uint8

const (
	// Next is an unconditional edge: to the next statement, a jump (break,
	// goto, return to the Exit), or back to a loop's head.
	Next EdgeKind = iota
	// True is taken when the last node's condition holds (a Condition), or
	// to run the body of a for loop (an Iteration).
	True
	// False is taken when the condition does not hold, or to leave a for
	// loop.
	False
)

func (k EdgeKind) String() string {
	switch k {
	case Next:
		return "next"
	case True:
		return "true"
	case False:
		return "false"
	}
	return fmt.Sprintf("EdgeKind(%d)", int(k))
}

// Edge is an edge of the graph.
type Edge struct {
	To   *Block
	Kind EdgeKind
}

// Block is a basic block: nodes run in order, entered at the first and
// left after the last.
type Block struct {
	// ID is the index of the block in Graph.Blocks.
	ID int
	// Nodes are the block's nodes, in order; none for the Exit, and maybe
	// none for a block that only joins or passes on.
	Nodes []*Node
	// Succs are the edges out of the block: one Next edge, or the True and
	// False edges of a block ending with a Condition or an Iteration (one
	// of them for a condition that is a constant), or none (the Exit, a
	// block ending with a goto without its label).
	Succs []Edge
	// Preds are the blocks with an edge to this one, one per edge.
	Preds []*Block
	// Reachable reports whether a path from the Entry leads to the block.
	Reachable bool
	// Graph is the graph of the block.
	Graph *Graph

	first int32 // the index of its first node, plus one; 0 for none
	npred int32
}

// Graph is the control-flow graph of a function: the main chunk or a
// FunctionExpr.
type Graph struct {
	// Func is the function's scope.
	Func *scope.Scope
	// Function is the function: nil for the main chunk.
	Function *ast.FunctionExpr
	// Parent is the graph of the function the function is in, nil for the
	// main chunk; Creator the node of Parent that evaluates the
	// FunctionExpr.
	Parent  *Graph
	Creator *Node
	// Entry is the first block, with the Enter node and no predecessors;
	// Exit the last one, which the returns and the end of the body go to.
	Entry, Exit *Block
	// Blocks are the blocks: Entry first, Exit second, then in the order
	// they are made (a loop's head before its body, the block after an if
	// before its branches' ends, a label's block when a goto first jumps
	// to it). A block's nodes are consecutive in Nodes.
	Blocks []*Block
	// Nodes are the nodes in source order (Node.Index).
	Nodes []*Node
	// ID is the index of the graph in Program.Graphs.
	ID int

	fallOff *Block // the block that reaches the end of the body
	alloc   *alloc
	slot    int32 // the expression of Creator it is created in
}

// alloc hands out the blocks and edges of a Program's graphs from slabs.
type alloc struct {
	blocks []Block
	edges  []Edge
	hint   int // the length of the source
}

func (g *Graph) newBlock() *Block {
	a := g.alloc
	if len(a.blocks) == cap(a.blocks) {
		a.blocks = make([]Block, 0, slabSize(cap(a.blocks), a.hint/109+8, 512))
	}
	a.blocks = a.blocks[:len(a.blocks)+1]
	b := &a.blocks[len(a.blocks)-1]
	b.ID, b.Graph = len(g.Blocks), g
	g.Blocks = append(g.Blocks, b)
	return b
}

// link adds an edge; the predecessors are made when the graph is done. A
// block has at most two successors.
func link(from, to *Block, kind EdgeKind) {
	if from.Succs == nil {
		a := from.Graph.alloc
		if cap(a.edges)-len(a.edges) < 2 {
			a.edges = make([]Edge, 0, slabSize(cap(a.edges), a.hint/50+8, 1024))
		}
		from.Succs = a.edges[len(a.edges) : len(a.edges) : len(a.edges)+2]
		a.edges = a.edges[:len(a.edges)+2]
	}
	from.Succs = append(from.Succs, Edge{To: to, Kind: kind})
}
