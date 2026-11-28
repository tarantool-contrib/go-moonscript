package scope

import (
	"fmt"
	"sort"
	"sync"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
)

// Dialect is the language of the tree: it decides the implicit "arg" of
// Lua 5.1's vararg functions and whether labels and gotos exist. Its
// values are those of lua.Dialect and parse.Dialect, so a conversion
// (scope.Dialect(parse.LuaJIT)) is enough.
type Dialect int

const (
	// Lua51 is PUC Lua 5.1: a vararg function (not the main chunk) has the
	// implicit local "arg" unless its ParList has NoArg; no labels.
	Lua51 Dialect = iota
	// LuaJIT is LuaJIT 2.1: no implicit "arg"; goto and labels.
	LuaJIT
)

func (d Dialect) String() string {
	switch d {
	case Lua51:
		return "Lua51"
	case LuaJIT:
		return "LuaJIT"
	}
	return fmt.Sprintf("Dialect(%d)", int(d))
}

// VarKind says how a variable was declared.
type VarKind uint8

const (
	// KindLocal is a name of "local a, b = ...".
	KindLocal VarKind = iota
	// KindLocalFunction is the name of "local function f".
	KindLocalFunction
	// KindParam is a named parameter of a function.
	KindParam
	// KindForNum is the variable of a numeric for.
	KindForNum
	// KindForIn is a variable of a generic for.
	KindForIn
	// KindSelf is the implicit "self" of a method ("function a.b:c()"); it
	// has no name in the source.
	KindSelf
	// KindArg is the implicit "arg" of a Lua51 vararg function; it has no
	// name in the source.
	KindArg
	// KindLabel is a label (LuaJIT).
	KindLabel
)

var kindNames = [...]string{"local", "local function", "parameter", "for variable", "for variable", "self", "arg", "label"}

func (k VarKind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return fmt.Sprintf("VarKind(%d)", int(k))
}

// Var is a declared variable or a label.
type Var struct {
	// Name is the name.
	Name string
	// Kind is how it was declared.
	Kind VarKind
	// Decl is the node that declares it: the *ast.LocalAssignStmt (also
	// for a local function), *ast.NumberForStmt, *ast.GenericForStmt,
	// *ast.LabelStmt, or for a parameter and the implicit self and arg the
	// *ast.FunctionExpr.
	Decl ast.Element
	// Index is the index of the name in the node's list of names (the
	// LocalAssignStmt's, the GenericForStmt's, the ParList's); 0 for a
	// numeric for's variable and a label, -1 for an implicit variable.
	Index int
	// NameSpan is the span of the name where it is declared; the zero Span
	// for an implicit variable and for a tree built without spans.
	NameSpan ast.Span
	// Scope is the scope the variable is declared in.
	Scope *Scope
	// Shadows is the variable that this one hides, nil if none (see the
	// package documentation).
	Shadows *Var
	// Reads and Writes are the occurrences of the name that refer to the
	// variable, in source order: those that read it and those that assign
	// it ("local x; x = 1" is a write). For a label they are the gotos
	// that jump to it (Reads) and none (Writes).
	Reads, Writes []*Ref
	// Captured is set if a function other than the one that declares the
	// variable refers to it: it is an upvalue of that function.
	Captured bool
	// ID is the index of the variable in Info.Vars.
	ID int

	nr, nw int32
}

// Implicit reports whether the variable has no name in the source: the
// "self" of a method and the "arg" of a vararg function.
func (v *Var) Implicit() bool { return v.Kind == KindSelf || v.Kind == KindArg }

// Used reports whether the variable is read, or for a label jumped to.
func (v *Var) Used() bool { return len(v.Reads) > 0 }

// Func returns the scope of the function that declares the variable.
func (v *Var) Func() *Scope { return v.Scope.Func }

// ScopeKind says what a scope is of.
type ScopeKind uint8

const (
	// ScopeFunction is a function: its parameters and its body's locals.
	// The root scope, the main function, is one.
	ScopeFunction ScopeKind = iota
	// ScopeDo is the body of a "do" block.
	ScopeDo
	// ScopeWhile is the body of a while loop.
	ScopeWhile
	// ScopeRepeat is the body of a repeat loop and its condition.
	ScopeRepeat
	// ScopeThen is the block of an if or elseif.
	ScopeThen
	// ScopeElse is the block of an else.
	ScopeElse
	// ScopeFor holds the variables of a for loop; its span is the body's.
	ScopeFor
	// ScopeForBody is the body of a for loop.
	ScopeForBody
)

var scopeNames = [...]string{"function", "do", "while", "repeat", "then", "else", "for", "for body"}

func (k ScopeKind) String() string {
	if int(k) < len(scopeNames) {
		return scopeNames[k]
	}
	return fmt.Sprintf("ScopeKind(%d)", int(k))
}

// Scope is a function or a block.
type Scope struct {
	Kind ScopeKind
	// Node is the element the scope is of: the *ast.FunctionExpr of a
	// function (the *ast.Chunk of the root, nil for ResolveStmts), the
	// statement of a block (an *ast.IfStmt for its then and else blocks;
	// an elseif is the IfStmt in the else).
	Node ast.Element
	// Span is where the scope's names can be referred to: the function's
	// span (the chunk's for the root), the block's span, the body of a
	// for for both of its scopes, a repeat's body to the end of its
	// condition. Zero for a tree built without spans.
	Span ast.Span
	// Parent is the enclosing scope, nil for the root.
	Parent *Scope
	// Children are the scopes directly inside, in order.
	Children []*Scope
	// Vars are the variables declared in the scope, in order of declaration:
	// for a function, "self", the parameters and "arg" first.
	Vars []*Var
	// Func is the scope of the function this scope is in: itself for a
	// function.
	Func *Scope
	// Upvalues, for a function, are the variables of enclosing functions that
	// it or a function inside it refers to, in the order of first
	// reference: the function prototype's upvalues, by name and order.
	Upvalues []*Var
	// ID is the index of the scope in Info.Scopes.
	ID int

	nv, nc int
}

// Contains reports whether the byte offset is in the scope's span.
func (s *Scope) Contains(offset int) bool {
	return s.Span.Start <= offset && offset < s.Span.End
}

// IsFunction reports whether the scope is a function.
func (s *Scope) IsFunction() bool { return s.Kind == ScopeFunction }

// RefKind says what an occurrence of a name does.
type RefKind uint8

const (
	// Read is a use of the value of a name.
	Read RefKind = iota
	// Write is an assignment to a name: a target "x" of an assignment, the
	// name of "function x()".
	Write
	// Jump is the label of a goto.
	Jump
)

func (k RefKind) String() string {
	switch k {
	case Read:
		return "read"
	case Write:
		return "write"
	case Jump:
		return "jump"
	}
	return fmt.Sprintf("RefKind(%d)", int(k))
}

// Ref is an occurrence of a name: a name in an expression or as the target
// of an assignment (Ident), or the label of a goto (Goto).
type Ref struct {
	Kind RefKind
	// Ident is the name's node; nil for a jump.
	Ident *ast.IdentExpr
	// Goto is the goto statement of a jump; nil for the others.
	Goto *ast.GotoStmt
	// Var is what the name refers to: nil for a global, and for the label
	// of a goto that has no label.
	Var *Var
	// Scope is the innermost scope the occurrence is in.
	Scope *Scope
}

// Name returns the name.
func (r *Ref) Name() string {
	if r.Ident != nil {
		return r.Ident.Value
	}
	if r.Goto != nil {
		return r.Goto.Label
	}
	return ""
}

// Span returns the span of the name (of a goto's label, not of the
// statement); the zero Span without a position.
func (r *Ref) Span() ast.Span {
	if r.Ident != nil {
		return r.Ident.Span()
	}
	if r.Goto != nil {
		return r.Goto.LabelSpan
	}
	return ast.Span{}
}

// IsGlobal reports whether the occurrence is a global: a name that refers
// to no variable, read or written.
func (r *Ref) IsGlobal() bool { return r.Var == nil && r.Kind != Jump }

// IsUpvalue reports whether the occurrence is in a function other than the
// one of the variable it refers to.
func (r *Ref) IsUpvalue() bool {
	return r.Var != nil && r.Kind != Jump && r.Var.Scope.Func != r.Scope.Func
}

// Global is a global name of the tree.
type Global struct {
	Name string
	// Reads and Writes are its occurrences, in source order.
	Reads, Writes []*Ref
}

// Problem is a label error of a tree: what LuaJIT's parser rejects, which
// a tree built by other code can have.
type Problem struct {
	// Node is the statement it is at.
	Node ast.Element
	// Span is the span of the name it is about.
	Span ast.Span
	// Msg is LuaJIT's message: "duplicate label 'x'", "undefined label
	// 'x'", "<goto x> jumps into the scope of local 'y'"; or why the
	// tree can't be resolved.
	Msg string
}

func (p Problem) String() string { return p.Msg }

// Error is what Resolve returns for a tree with problems.
type Error struct {
	Problems []Problem
}

func (e *Error) Error() string {
	if len(e.Problems) == 1 {
		return "scope: " + e.Problems[0].Msg
	}
	return fmt.Sprintf("scope: %s (and %d more problems)", e.Problems[0].Msg, len(e.Problems)-1)
}

// Info is the result of Resolve. All its fields are read only.
type Info struct {
	// Dialect is the dialect it was resolved for.
	Dialect Dialect
	// Root is the main function's scope.
	Root *Scope
	// Scopes are all scopes, the root first, a scope before its children,
	// siblings in source order.
	Scopes []*Scope
	// Vars are all variables and labels in the order their names appear in
	// the source (an implicit self before the parameters, an arg after
	// them).
	Vars []*Var
	// Refs are all the occurrences of names and the gotos, in source order.
	Refs []Ref
	// Globals are the global names, in the order of their first
	// occurrence.
	Globals []*Global
	// Shadowing are the variables that shadow another, in Vars' order.
	Shadowing []*Var
	// Problems are the errors in labels.
	Problems []Problem

	chunk *ast.Chunk

	refIdxOnce sync.Once
	refIdx     map[*ast.IdentExpr]int32
	offOnce    sync.Once
	refsByOff  []int32 // indices of Refs by span start, if Refs are not sorted
	varsByOff  []*Var  // the Vars with a name span, by start
	nodeOnce   sync.Once
	nodeScope  map[ast.Element]*Scope
}

// Chunk returns the chunk that was resolved, nil for ResolveStmts.
func (in *Info) Chunk() *ast.Chunk { return in.chunk }

// RefOf returns the occurrence of the name node, nil if the node is not in
// the tree. The first call builds an index.
func (in *Info) RefOf(id *ast.IdentExpr) *Ref {
	in.refIdxOnce.Do(func() {
		in.refIdx = make(map[*ast.IdentExpr]int32, len(in.Refs))
		for i := range in.Refs {
			if id := in.Refs[i].Ident; id != nil {
				in.refIdx[id] = int32(i)
			}
		}
	})
	if i, ok := in.refIdx[id]; ok {
		return &in.Refs[i]
	}
	return nil
}

// VarOf returns the variable that the name node refers to, nil for a
// global or a node that is not in the tree.
func (in *Info) VarOf(id *ast.IdentExpr) *Var {
	if r := in.RefOf(id); r != nil {
		return r.Var
	}
	return nil
}

func (in *Info) buildOffsets() {
	in.offOnce.Do(func() {
		sorted := true
		prev := -1
		for i := range in.Refs {
			s := in.Refs[i].Span().Start
			if s < prev {
				sorted = false
				break
			}
			prev = s
		}
		if !sorted {
			in.refsByOff = make([]int32, len(in.Refs))
			for i := range in.refsByOff {
				in.refsByOff[i] = int32(i)
			}
			sort.SliceStable(in.refsByOff, func(a, b int) bool {
				return in.Refs[in.refsByOff[a]].Span().Start < in.Refs[in.refsByOff[b]].Span().Start
			})
		}
		for _, v := range in.Vars {
			if v.NameSpan.End > v.NameSpan.Start {
				in.varsByOff = append(in.varsByOff, v)
			}
		}
		sort.SliceStable(in.varsByOff, func(a, b int) bool {
			return in.varsByOff[a].NameSpan.Start < in.varsByOff[b].NameSpan.Start
		})
	})
}

// RefAt returns the occurrence of a name whose span contains the byte
// offset of the source, nil if there is none. It is for parsed trees (a
// tree built without spans has no offsets).
func (in *Info) RefAt(offset int) *Ref {
	in.buildOffsets()
	n := len(in.Refs)
	start := func(i int) int {
		if in.refsByOff != nil {
			return in.Refs[in.refsByOff[i]].Span().Start
		}
		return in.Refs[i].Span().Start
	}
	i := sort.Search(n, func(i int) bool { return start(i) > offset }) - 1
	for ; i >= 0; i-- { // spans don't overlap; skip the zero spans
		j := i
		if in.refsByOff != nil {
			j = int(in.refsByOff[i])
		}
		sp := in.Refs[j].Span()
		if sp.End > sp.Start && sp.Start <= offset && offset < sp.End {
			return &in.Refs[j]
		}
		if sp.End > sp.Start {
			break
		}
	}
	return nil
}

// VarAt returns the variable whose declared name contains the byte
// offset, nil if there is none (implicit variables have no span).
func (in *Info) VarAt(offset int) *Var {
	in.buildOffsets()
	vs := in.varsByOff
	i := sort.Search(len(vs), func(i int) bool { return vs[i].NameSpan.Start > offset }) - 1
	if i >= 0 && offset < vs[i].NameSpan.End {
		return vs[i]
	}
	return nil
}

// ScopeAt returns the innermost scope whose span contains the byte offset,
// the root if there is none deeper.
func (in *Info) ScopeAt(offset int) *Scope {
	s := in.Root
	for {
		var next *Scope
		for _, c := range s.Children {
			if c.Contains(offset) {
				next = c
			}
		}
		if next == nil {
			return s
		}
		s = next
	}
}

// ScopeOf returns the scope of a node: a function's (*ast.FunctionExpr,
// *ast.Chunk), a block statement's (the first of its scopes: a for's
// ScopeFor, an if's then block), nil if the node has none. The first call
// builds an index.
func (in *Info) ScopeOf(n ast.Element) *Scope {
	in.nodeOnce.Do(func() {
		in.nodeScope = make(map[ast.Element]*Scope, len(in.Scopes))
		for _, s := range in.Scopes {
			if s.Node != nil {
				if _, ok := in.nodeScope[s.Node]; !ok {
					in.nodeScope[s.Node] = s
				}
			}
		}
	})
	return in.nodeScope[n]
}

// Global returns the global of the name, nil if the tree has none.
func (in *Info) Global(name string) *Global {
	for _, g := range in.Globals {
		if g.Name == name {
			return g
		}
	}
	return nil
}
