// Package grammar describes the MoonScript v0.8.0 grammar as a tree of PEG
// combinators with pgen semantics. It is pure data: the parser in
// moonscript/parse is generated from it (internal/tools/genparser), and
// the parse tests interpret it directly as a differential oracle for the
// generated code.
//
// Callbacks (Cfn transforms, Cmt match-time functions) are referenced by
// the name of the Go function in package parse that implements them.
package grammar

import "sort"

// Op is a combinator kind.
type Op uint8

// Combinators. The comments give the pgen/lpeg notation and the Node fields
// each one uses.
const (
	OpLit  Op = iota + 1 // P"str": Str
	OpTrie               // P"a" + P"b" + ... compiled to a byte trie: Strs
	OpAny                // P(n): N
	OpSet                // S"..." / R(...): Set
	OpSeq                // a * b * ...: Kids
	OpAlt                // a + b + ...: Kids
	OpDAlt               // FIRST-byte dispatched a + b + ...: ID (pgen choice number), Kids
	OpRep                // patt^n, n >= 0: N, Kids[0]
	OpOpt                // patt^-1: Kids[0]
	OpNot                // -patt: Kids[0]
	OpLook               // L(patt): Kids[0]
	OpRule               // V"name": Name

	OpC   // C(patt): Kids[0]
	OpCc  // Cc(vals...): Vals (string, int, bool or nil)
	OpCp  // Cp()
	OpCt  // Ct(patt): Kids[0]
	OpCg  // Cg(patt, name): Kids[0], Name
	OpCfn // patt / fn: Kids[0], Name (FnCallback)
	OpCmb // Cmb(name), back reference to a named group: Name
	OpCmt // Cmt(patt, fn): Kids[0], Name (CmtCallback)
	OpPos // Cfn(Seq(Cp(), patt), "posFn"): Kids[0]

	// pgen.indenter operations: Stack, TabWidth (and N)
	OpIndCheck   // check
	OpIndAdvance // advance
	OpIndPush    // push
	OpIndPrevent // prevent
	OpIndPop     // pop
	OpIndCPush   // cpush(N)
	OpIndCTopNe  // ctop_ne(N)
)

// Node is one combinator.
type Node struct {
	Op       Op
	Kids     []*Node
	Str      string
	Strs     []string
	N        int
	ID       int
	Set      *[256]bool
	Vals     []any
	Name     string
	Stack    int
	TabWidth int
}

// Grammar is a set of named rules.
type Grammar struct {
	Start string
	Rules map[string]*Node
	// Memoized lists the position-pure rules pgen gives a single-slot memo
	// (memo_pos/memo_end in slow.lua), in slot order. They must not capture
	// or touch the indentation stacks.
	Memoized []string
	// Packrat lists rules the parser memoizes on top of pgen's semantics,
	// invisibly (see parse/packrat.go); at most 8.
	Packrat []string
}

// RuleNames returns the rule names in sorted order.
func (g *Grammar) RuleNames() []string {
	names := make([]string, 0, len(g.Rules))
	for n := range g.Rules {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// MemoSlot returns the memo slot of a rule, or -1.
func (g *Grammar) MemoSlot(name string) int {
	for i, m := range g.Memoized {
		if m == name {
			return i
		}
	}
	return -1
}

// --- constructors ------------------------------------------------------------

// Lit matches a literal string.
func Lit(s string) *Node { return &Node{Op: OpLit, Str: s} }

// Trie is an ordered choice made only of literals (see parse for the
// semantics pgen gives it).
func Trie(words ...string) *Node { return &Node{Op: OpTrie, Strs: words} }

// Any matches n arbitrary bytes.
func Any(n int) *Node { return &Node{Op: OpAny, N: n} }

// EOF is P(-1).
func EOF() *Node { return Not(Any(1)) }

// Set is S"chars".
func Set(chars string) *Node {
	var set [256]bool
	for i := 0; i < len(chars); i++ {
		set[chars[i]] = true
	}
	return &Node{Op: OpSet, Set: &set}
}

// Range is R("az", "AZ", ...).
func Range(ranges ...string) *Node {
	var set [256]bool
	for _, r := range ranges {
		for c := int(r[0]); c <= int(r[1]); c++ {
			set[c] = true
		}
	}
	return &Node{Op: OpSet, Set: &set}
}

// Seq is a * b * ...
func Seq(ps ...*Node) *Node {
	if len(ps) == 1 {
		return ps[0]
	}
	return &Node{Op: OpSeq, Kids: ps}
}

// Alt is the ordered choice a + b + ...
func Alt(ps ...*Node) *Node { return &Node{Op: OpAlt, Kids: ps} }

// DAlt is an ordered choice pgen compiles with FIRST-byte dispatch; id is
// the pgen choice number (disp[id] in slow.lua).
func DAlt(id int, ps ...*Node) *Node { return &Node{Op: OpDAlt, ID: id, Kids: ps} }

// Rep is patt^min.
func Rep(sub *Node, min int) *Node { return &Node{Op: OpRep, N: min, Kids: []*Node{sub}} }

// Opt is patt^-1.
func Opt(sub *Node) *Node { return &Node{Op: OpOpt, Kids: []*Node{sub}} }

// Not is -patt.
func Not(sub *Node) *Node { return &Node{Op: OpNot, Kids: []*Node{sub}} }

// Look is L(patt).
func Look(sub *Node) *Node { return &Node{Op: OpLook, Kids: []*Node{sub}} }

// Diff is a - b.
func Diff(a, b *Node) *Node { return Seq(Not(b), a) }

// V references a rule.
func V(name string) *Node { return &Node{Op: OpRule, Name: name} }

// C is a simple string capture.
func C(sub *Node) *Node { return &Node{Op: OpC, Kids: []*Node{sub}} }

// Cc is a constant capture.
func Cc(vals ...any) *Node { return &Node{Op: OpCc, Vals: vals} }

// Cp is a position capture.
func Cp() *Node { return &Node{Op: OpCp} }

// Ct is a table capture.
func Ct(sub *Node) *Node { return &Node{Op: OpCt, Kids: []*Node{sub}} }

// Cg is a named group capture.
func Cg(sub *Node, name string) *Node { return &Node{Op: OpCg, Name: name, Kids: []*Node{sub}} }

// Cfn is a function capture; fn names a parse.FnCallback.
func Cfn(sub *Node, fn string) *Node { return &Node{Op: OpCfn, Name: fn, Kids: []*Node{sub}} }

// Cmb matches the text of the most recent visible named group.
func Cmb(name string) *Node { return &Node{Op: OpCmb, Name: name} }

// Cmt is a match-time capture; fn names a parse.CmtCallback.
func Cmt(sub *Node, fn string) *Node { return &Node{Op: OpCmt, Name: fn, Kids: []*Node{sub}} }

// Pos is grammar.moon's pos helper, Cfn(Seq(Cp(), patt), "posFn"): the
// first value of patt, with its node position set. It is a combinator of
// its own so the generated parser can specialize it.
func Pos(patt *Node) *Node { return &Node{Op: OpPos, Kids: []*Node{patt}} }

// Indenter mirrors pgen.indenter.
type Indenter struct {
	Stack    int
	TabWidth int
}

func (in Indenter) op(op Op, n int) *Node {
	return &Node{Op: op, Stack: in.Stack, TabWidth: in.TabWidth, N: n}
}

// Check consumes indentation whose width must equal the top of the stack.
func (in Indenter) Check() *Node { return in.op(OpIndCheck, 0) }

// Advance pushes the indentation width if deeper than the top.
func (in Indenter) Advance() *Node { return in.op(OpIndAdvance, 0) }

// Push consumes indentation and pushes its width.
func (in Indenter) Push() *Node { return in.op(OpIndPush, 0) }

// Prevent pushes a sentinel so a nested Advance fails.
func (in Indenter) Prevent() *Node { return in.op(OpIndPrevent, 0) }

// Pop pops the stack.
func (in Indenter) Pop() *Node { return in.op(OpIndPop, 0) }

// CPush pushes a constant.
func (in Indenter) CPush(v int) *Node { return in.op(OpIndCPush, v) }

// CTopNe succeeds when the top differs from v.
func (in Indenter) CTopNe(v int) *Node { return in.op(OpIndCTopNe, v) }
