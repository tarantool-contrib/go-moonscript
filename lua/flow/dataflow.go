package flow

import (
	"fmt"
	"sync"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

// Program is the result of Analyze: the graphs of the functions of a tree
// and the data flow over its local variables. It is read only and safe
// for concurrent use.
type Program struct {
	// Info is the resolution the program was built from.
	Info *scope.Info
	// Graphs are the graphs of the functions: the main chunk first, then
	// the FunctionExprs in source order (the order of their scopes in
	// Info.Scopes).
	Graphs []*Graph
	// Defs are the values assigned to local variables, in the order they
	// are met: a statement's after those of the functions in its
	// expressions.
	Defs []*Def
	// Uses are the occurrences of local variables that read or mutate
	// them, in source order.
	Uses []*Use
	// Empty are the empty blocks, in source order.
	Empty []Empty

	varDefs [][]*Def
	varUses [][]*Use
	graphOf []*Graph // by scope ID, for the function scopes
	misses  int

	luacheckParens bool

	useOnce sync.Once
	useIdx  map[*ast.IdentExpr]*Use
}

// Main returns the graph of the main chunk.
func (p *Program) Main() *Graph { return p.Graphs[0] }

// Graph returns the graph of the function that a scope is in, nil if the
// scope is not of the tree.
func (p *Program) Graph(s *scope.Scope) *Graph {
	if s == nil || s.Func == nil || s.Func.ID >= len(p.graphOf) || p.Info.Scopes[s.Func.ID] != s.Func {
		return nil
	}
	return p.graphOf[s.Func.ID]
}

// VarDefs returns the values assigned to a variable: its declaration's
// (Init) and those of the assignments, in the order of Defs.
func (p *Program) VarDefs(v *scope.Var) []*Def {
	if v == nil || v.ID >= len(p.varDefs) {
		return nil
	}
	return p.varDefs[v.ID]
}

// VarUses returns the uses of a variable, in source order.
func (p *Program) VarUses(v *scope.Var) []*Use {
	if v == nil || v.ID >= len(p.varUses) {
		return nil
	}
	return p.varUses[v.ID]
}

// UseOf returns the use of the name node, nil if it is no use of a local
// (a global, an assigned name). The first call builds an index.
func (p *Program) UseOf(id *ast.IdentExpr) *Use {
	p.useOnce.Do(func() {
		p.useIdx = make(map[*ast.IdentExpr]*Use, len(p.Uses))
		for _, u := range p.Uses {
			p.useIdx[u.Ident] = u
		}
	})
	return p.useIdx[id]
}

// Def is a value assigned to a local variable: the declaration's of a
// local, a parameter (at Enter), a loop variable (at each Iteration), and
// those of the assignments to the variable (also "function f()" for a
// local f). A node that assigns a variable twice ("x, x = 1, 2") has two
// Defs; the last is the one that stays.
type Def struct {
	// Var is the variable.
	Var *scope.Var
	// Node is the node that assigns the value.
	Node *Node
	// Name is the name assigned in an assignment or a function statement;
	// nil for a declaration's value, whose name is at Var.NameSpan.
	Name *ast.IdentExpr
	// Value is the expression whose value is assigned; for a name after
	// the end of the list of expressions, the call or "..." at its end
	// that the value is unpacked from, or nil. Nil for parameters and loop
	// variables.
	Value ast.Expr
	// Init is set for the value of the declaration.
	Init bool
	// Empty is set for the value of a local declared without one ("local
	// x", "b" of "local a, b = 1"): nil, which the program didn't write.
	Empty bool
	// Used is set if a read of the variable may see the value, Mutated if
	// an assignment to a field of it may (see Use). A read in "t.k = e",
	// with one target and one value and no call, or in the function of
	// "function t.f()" or "function t:m()", doesn't make t's value used
	// through that statement, as a read in luacheck's "circular"
	// assignments doesn't.
	Used, Mutated bool
	// Overwritten, for a value that is neither used nor mutated, is the
	// assignment that every path from it reaches before the end of the
	// variable's scope, if there is one: the value is overwritten before
	// use. Nil otherwise.
	Overwritten *Def
	// ID is the index of the def in Program.Defs.
	ID int

	users []*Graph // for a function value: the functions using it
}

func (d *Def) isFunc() bool {
	fe, ok := d.Value.(*ast.FunctionExpr)
	return ok && fe != nil
}

// parenFunction reports whether a function value is in parentheses at the
// end of an assignment's values.
func parenFunction(d *Def, fe *ast.FunctionExpr) bool {
	st, ok := d.Node.Stmt.(*ast.AssignStmt)
	return ok && fe.Parens().Count > 0 && len(st.Rhs) > 0 && st.Rhs[len(st.Rhs)-1] == ast.Expr(fe)
}

func (d *Def) addUser(g *Graph) {
	for _, h := range d.users {
		if h == g {
			return
		}
	}
	d.users = append(d.users, g)
}

// Span returns the span of the name the value is assigned to: Name's,
// else that of the declared name (the zero Span for an implicit
// variable).
func (d *Def) Span() ast.Span {
	if d.Name != nil {
		return d.Name.Span()
	}
	return d.Var.NameSpan
}

func (d *Def) String() string {
	return fmt.Sprintf("def %s at %v", d.Var.Name, d.Span())
}

// UseKind says how a use uses its variable.
type UseKind uint8

const (
	// Read reads the variable's value.
	Read UseKind = iota
	// Mutate reads the variable to assign a field of its value: t in
	// "t.k = v", "t[k] = v", "t.a.b = v", "function t.f()", "function
	// t:m()".
	Mutate
)

func (k UseKind) String() string {
	switch k {
	case Read:
		return "read"
	case Mutate:
		return "mutate"
	}
	return fmt.Sprintf("UseKind(%d)", int(k))
}

// Use is an occurrence of a local variable that reads it.
type Use struct {
	// Var is the variable.
	Var *scope.Var
	// Ident is the name.
	Ident *ast.IdentExpr
	// Kind is how it uses the variable.
	Kind UseKind
	// Node is the node the name is in, in the graph of the function the
	// name is in.
	Node *Node
	// Defs are the values the use may see, in the order of Program.Defs
	// (see the package documentation for the upvalues). The uses of a
	// variable in one node share the slice, and so do those inside one
	// closure of the variable's function.
	Defs []*Def
	// ID is the index of the use in Program.Uses.
	ID int

	slot int32 // the expression of the node it is in (Node.slot)
}

// Uninitialized reports whether the values the use may see are all those
// of declarations without a value: the variable is used before any
// assignment can reach it. A use that no value reaches (in unreachable
// code) is not.
func (u *Use) Uninitialized() bool {
	if len(u.Defs) == 0 {
		return false
	}
	for _, d := range u.Defs {
		if !d.Empty {
			return false
		}
	}
	return true
}
