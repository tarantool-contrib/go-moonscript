package flow

import (
	"math/bits"
	"slices"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/scope"
)

// The data flow over the locals connects each use of a variable with the
// values it may see; the package documentation gives the rules. For a
// function and a variable declared in it, the defs and uses in the
// function's own nodes are the main ones; those in the functions inside
// it, the closures, are taken in bulk per closure, at the node that
// creates the closure (Graph.Creator, in the variable's function).

// group is uses of a variable that see the same values: those in one node
// of the variable's function, or those in one closure of it.
type group struct {
	node         *Node  // the node, or the closure's Creator
	clo          *Graph // the closure, nil for a node
	slot         int32  // for a node: the expression of the uses
	u0, u1       int    // the uses, in the variable's VarUses
	read, mutate bool
	circ         bool // the node is a circular assignment of the variable
}

// cdef is a def made in a closure of the variable's function.
type cdef struct {
	clo *Graph
	d   *Def
}

// varFlow is a variable of the function being analyzed.
type varFlow struct {
	v        *scope.Var
	mainDefs []*Def // in node order
	decl     *Def   // the declaration's value
	// kills are the nodes where luacheck ends the variable's scope
	// (Options.luacheckScopes), in node order
	kills []*Node
}

type flowState struct {
	p *Program
	g *Graph

	noProblems bool
	words      int        // words of a bitset of the blocks
	reach      [][]uint64 // by block: the blocks reachable from its successors

	// luacheck's cross-resolution of closures (Options.luacheckClosures)
	crossAll bool
	// luacheck's reads in circular assignments (Options.luacheckCircular)
	circularReads bool
	// luacheck's ends of scopes (Options.luacheckScopes): by Var.ID, the
	// nodes declaring a variable of the same name in the same scope
	redecl map[int][]*Node

	// the reaching definitions of the variable being analyzed (solve)
	w       int      // words of a set of its main values
	inOf    []int32  // by block: the offset of its set in sets, -1 for none
	evNode  []int32  // by block: the node of its last value or end, -1 for none
	evDef   []int32  // by block: the index of that value, -1 for an end
	sets    []uint64 // the sets
	touched []int32  // the blocks of inOf, evNode set
	work    []int32
	one     []uint64
	stack   []*Block
	// the blocks an overwriter walk has seen, by stamp
	seen      []uint32
	seenStamp uint32

	// the variable being analyzed
	mark   []uint32 // by Def.ID: the stamp of the group that took it
	stamp  uint32
	soft   []*Def // the values a group sees, which a circularity voids
	hard   []*Def // the others
	arena  []*Def
	cdefs  []cdef
	groups []group
}

func (p *Program) flow(opts *Options) {
	// the variables of each graph: counted, then carved
	count := make([]int32, len(p.Graphs)+1)
	graphOf := func(v *scope.Var) *Graph {
		if v.Kind == scope.KindLabel || v.Scope == nil || v.ID >= len(p.varDefs) {
			return nil
		}
		if len(p.varDefs[v.ID]) == 0 && len(p.varUses[v.ID]) == 0 {
			return nil
		}
		return p.Graph(v.Scope)
	}
	for _, v := range p.Info.Vars {
		if g := graphOf(v); g != nil {
			count[g.ID+1]++
		}
	}
	for i := 1; i < len(count); i++ {
		count[i] += count[i-1]
	}
	all := make([]*scope.Var, count[len(count)-1])
	byGraph := make([][]*scope.Var, len(p.Graphs))
	for i := range byGraph {
		byGraph[i] = all[count[i]:count[i]:count[i+1]]
	}
	for _, v := range p.Info.Vars {
		if g := graphOf(v); g != nil {
			byGraph[g.ID] = append(byGraph[g.ID], v)
		}
	}
	f := &flowState{
		p:             p,
		noProblems:    len(p.Info.Problems) == 0,
		crossAll:      opts != nil && opts.luacheckClosures,
		circularReads: opts != nil && opts.luacheckCircular,
		mark:          make([]uint32, len(p.Defs)),
	}
	if opts != nil && opts.luacheckScopes {
		f.redecl = map[int][]*Node{}
		for _, w := range p.Info.Vars {
			if s := w.Shadows; s != nil && sameLuacheckScope(w, s) {
				for _, d := range p.varDefs[w.ID] {
					if d.Init {
						f.redecl[s.ID] = append(f.redecl[s.ID], d.Node)
						break
					}
				}
			}
		}
	}
	for i, vars := range byGraph {
		if len(vars) > 0 {
			f.graph(p.Graphs[i], vars)
		}
	}
}

// sameLuacheckScope reports whether two variables are in the same scope for
// luacheck: the same scope, or a for's variables and its body's locals.
func sameLuacheckScope(w, s *scope.Var) bool {
	if w.Scope == s.Scope {
		return true
	}
	return (s.Kind == scope.KindForNum || s.Kind == scope.KindForIn) &&
		w.Scope.Kind == scope.ScopeForBody && w.Scope.Parent == s.Scope
}

// child returns the closure of g that h is, or is in; nil if none.
func child(g, h *Graph) *Graph {
	for h != nil && h.Parent != g {
		h = h.Parent
	}
	return h
}

// reachable returns the blocks reachable from the successors of a block.
func (f *flowState) reachable(b *Block) []uint64 {
	if f.reach == nil {
		f.reach = make([][]uint64, len(f.g.Blocks))
	}
	if r := f.reach[b.ID]; r != nil {
		return r
	}
	r := make([]uint64, f.words)
	stack := append(f.stack[:0], b)
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range x.Succs {
			id := e.To.ID
			if r[id>>6]&(1<<(id&63)) == 0 {
				r[id>>6] |= 1 << (id & 63)
				stack = append(stack, e.To)
			}
		}
	}
	f.reach[b.ID] = r
	f.stack = stack
	return r
}

// reaches reports whether a path leads from a node to another: after it,
// or from it with inclusive.
func (f *flowState) reaches(from, to *Node, inclusive bool) bool {
	if from.Block == to.Block && (to.Index > from.Index || inclusive && to.Index == from.Index) {
		return true
	}
	r := f.reachable(from.Block)
	return r[to.Block.ID>>6]&(1<<(to.Block.ID&63)) != 0
}

// onlyDefReaches reports whether the value of the only main def of a
// variable reaches a node. In a parsed tree the declaration is on every
// path from the Entry into the variable's scope (no goto jumps into it),
// so it reaches every node of the scope that the Entry reaches.
func (f *flowState) onlyDefReaches(d *Def, to *Node) bool {
	if f.noProblems && d.Init && d.Node.Block.Reachable && to.Block.Reachable {
		return true
	}
	return f.reaches(d.Node, to, false)
}

// graph analyzes the variables declared in a function.
func (f *flowState) graph(g *Graph, vars []*scope.Var) {
	f.g = g
	f.words = (len(g.Blocks) + 63) / 64
	f.reach = nil // made by reachable
	var vf varFlow
	for _, v := range vars {
		defs := f.p.varDefs[v.ID]
		vf = varFlow{v: v, mainDefs: defs}
		for i, d := range defs {
			if d.Node.Block.Graph != g { // some are in closures
				vf.mainDefs = make([]*Def, i, len(defs))
				copy(vf.mainDefs, defs)
				for _, d := range defs[i:] {
					if d.Node.Block.Graph == g {
						vf.mainDefs = append(vf.mainDefs, d)
					}
				}
				break
			}
		}
		for _, d := range vf.mainDefs {
			if d.Init {
				vf.decl = d
				break
			}
		}
		if f.redecl != nil {
			for _, n := range f.redecl[v.ID] {
				if n.Block.Graph == g {
					vf.kills = append(vf.kills, n)
				}
			}
		}
		if len(vf.mainDefs) > 1 || len(vf.kills) > 0 {
			f.solve(&vf)
		}
		f.variable(&vf)
	}
	for _, id := range f.touched {
		f.inOf[id], f.evNode[id] = -1, -1
	}
	f.touched = f.touched[:0]
	f.inOf, f.evNode, f.evDef = nil, nil, nil
}

// solve finds the main values of a variable with several of them (or an
// end of scope) that reach the start of each block of its scope: f.inOf,
// offsets in f.sets of sets of indices of mainDefs. It walks the blocks
// that the values lead to without leaving the variable's scope: a path
// out of the scope comes back through the declaration, which kills them.
func (f *flowState) solve(vf *varFlow) {
	g := f.g
	if f.inOf == nil || len(f.inOf) < len(g.Blocks) {
		f.inOf = make([]int32, len(g.Blocks))
		f.evNode = make([]int32, len(g.Blocks))
		f.evDef = make([]int32, len(g.Blocks))
		for i := range f.inOf {
			f.inOf[i], f.evNode[i] = -1, -1
		}
	}
	for _, id := range f.touched {
		f.inOf[id], f.evNode[id] = -1, -1
	}
	f.touched, f.sets, f.work = f.touched[:0], f.sets[:0], f.work[:0]
	w := (len(vf.mainDefs) + 63) / 64
	f.w = w
	// the last event of each block: a value (its index) or an end (-1)
	event := func(b *Block, node, def int32) {
		if f.evNode[b.ID] < 0 && f.inOf[b.ID] < 0 {
			f.touched = append(f.touched, int32(b.ID))
		}
		if node >= f.evNode[b.ID] {
			f.evNode[b.ID], f.evDef[b.ID] = node, def
		}
	}
	for i, d := range vf.mainDefs {
		event(d.Node.Block, int32(d.Node.Index), int32(i))
	}
	for _, k := range vf.kills {
		event(k.Block, int32(k.Index), -1)
	}
	for _, id := range f.touched {
		if f.evDef[id] >= 0 {
			f.work = append(f.work, id)
		}
	}
	var one []uint64 // the set of a block's last value
	if cap(f.one) < w {
		f.one = make([]uint64, w)
	}
	one = f.one[:w]
	for len(f.work) > 0 {
		id := f.work[len(f.work)-1]
		f.work = f.work[:len(f.work)-1]
		b := g.Blocks[id]
		var out []uint64
		if f.evNode[id] >= 0 {
			clear(one)
			d := f.evDef[id]
			one[d>>6] |= 1 << (d & 63)
			out = one
		} else {
			o := f.inOf[id]
			out = f.sets[o : o+int32(w)]
		}
		for _, e := range b.Succs {
			s := e.To
			if len(s.Nodes) > 0 && !inScope(s.Nodes[0], vf.v, vf.decl) {
				continue
			}
			o := f.inOf[s.ID]
			if o < 0 {
				if f.evNode[s.ID] < 0 {
					f.touched = append(f.touched, int32(s.ID))
				}
				o = int32(len(f.sets))
				f.inOf[s.ID] = o
				f.sets = append(f.sets, make([]uint64, w)...)
				if f.evNode[id] < 0 { // f.sets may have moved
					out = f.sets[f.inOf[id] : f.inOf[id]+int32(w)]
				}
			}
			in := f.sets[o : o+int32(w)]
			changed := false
			for i := range in {
				if x := in[i] | out[i]; x != in[i] {
					in[i] = x
					changed = true
				}
			}
			if changed && f.evNode[s.ID] < 0 {
				f.work = append(f.work, int32(s.ID))
			}
		}
	}
}

func setRange(s []uint64, lo, hi int) {
	for i := lo; i < hi; i++ {
		s[i>>6] |= 1 << (i & 63)
	}
}

func clearRange(s []uint64, lo, hi int) {
	for i := lo; i < hi; i++ {
		s[i>>6] &^= 1 << (i & 63)
	}
}

// reachingAt appends to dst the main defs of a variable that reach a node
// (before the node's own defs).
func (f *flowState) reachingAt(dst []*Def, vf *varFlow, n *Node) []*Def {
	switch len(vf.mainDefs) {
	case 0:
		return dst
	case 1:
		if len(vf.kills) == 0 {
			if d := vf.mainDefs[0]; f.onlyDefReaches(d, n) {
				dst = append(dst, d)
			}
			return dst
		}
	}
	// the last def before the node in its block, else the block's start
	defs := vf.mainDefs
	i, _ := slices.BinarySearchFunc(defs, n.Index, func(d *Def, idx int) int { return d.Node.Index - idx })
	var last *Def
	if i > 0 && defs[i-1].Node.Block == n.Block {
		last = defs[i-1] // the last def of its node is the one that stays
	}
	if len(vf.kills) > 0 {
		k, _ := slices.BinarySearchFunc(vf.kills, n.Index, func(m *Node, idx int) int { return m.Index - idx })
		if k > 0 && vf.kills[k-1].Block == n.Block && (last == nil || vf.kills[k-1].Index >= last.Node.Index) {
			return dst
		}
	}
	if last != nil {
		return append(dst, last)
	}
	o := f.inOf[n.Block.ID]
	if o < 0 {
		return dst
	}
	for i, x := range f.sets[o : o+int32(f.w)] {
		for x != 0 {
			b := bits.TrailingZeros64(x)
			x &^= 1 << b
			dst = append(dst, defs[i*64+b])
		}
	}
	return dst
}

// variable connects the uses of a variable with its values.
func (f *flowState) variable(vf *varFlow) {
	v, g := vf.v, f.g
	defs := f.p.varDefs[v.ID]
	uses := f.p.varUses[v.ID]
	f.cdefs = f.cdefs[:0]
	for _, d := range defs {
		if !lastOfNode(d) {
			continue // overwritten in its own node
		}
		if h := d.Node.Block.Graph; h != g {
			if c := child(g, h); c != nil && c.Creator != nil {
				f.cdefs = append(f.cdefs, cdef{c, d})
			}
		}
	}
	// the groups of uses
	f.groups = f.groups[:0]
	for i, u := range uses {
		var node *Node
		var clo *Graph
		slot := int32(0)
		if h := u.Node.Block.Graph; h == g {
			node, slot = u.Node, u.slot
		} else {
			clo = child(g, h)
			if clo == nil || clo.Creator == nil {
				continue
			}
			node = clo.Creator
		}
		if k := len(f.groups) - 1; k < 0 || f.groups[k].node != node || f.groups[k].clo != clo || f.groups[k].slot != slot || f.groups[k].u1 != i {
			f.groups = append(f.groups, group{node: node, clo: clo, slot: slot, u0: i, u1: i, circ: node.circular == v})
		}
		gr := &f.groups[len(f.groups)-1]
		gr.u1 = i + 1
		if u.Kind == Mutate {
			gr.mutate = true
		} else {
			gr.read = true
		}
	}
	for gi := range f.groups {
		gr := &f.groups[gi]
		f.soft, f.hard = f.soft[:0], f.hard[:0]
		n := gr.node
		// the main values reaching the node (for a closure, its creation),
		// but not the declaration's closure ("local function f"): the
		// declaration makes a new instance of the variable, which the
		// values before it are not of
		if gr.clo == nil || vf.decl == nil || n != vf.decl.Node {
			f.soft = f.reachingAt(f.soft, vf, n)
		}
		if gr.clo == nil {
			// the values of closures created before the uses: in an
			// expression before theirs in the node, or in a node before
			for _, cd := range f.cdefs {
				if c := cd.clo.Creator; c == n && cd.clo.slot <= gr.slot || f.reaches(c, n, false) {
					f.hard = append(f.hard, cd.d)
				}
			}
		} else {
			// the main values assigned after the closure is created, which
			// it may see when called later
			for _, d := range vf.mainDefs {
				if lastOfNode(d) && f.reaches(n, d.Node, true) {
					f.hard = append(f.hard, d)
				}
			}
			// the values of closures that may run before or after it
			for _, cd := range f.cdefs {
				c := cd.clo.Creator
				if f.crossAll && g.Parent != nil || f.reaches(n, c, true) || f.reaches(c, n, true) {
					f.hard = append(f.hard, cd.d)
				}
			}
		}
		f.connect(gr, uses[gr.u0:gr.u1])
	}
	f.overwritten(vf, defs)
}

// connect gives the uses of a group the values in f.soft and f.hard, and
// marks the values used or mutated.
func (f *flowState) connect(gr *group, uses []*Use) {
	f.stamp++
	if f.stamp == 0 { // wrapped: clear the marks
		clear(f.mark)
		f.stamp = 1
	}
	start := len(f.arena)
	add := func(d *Def) {
		if f.mark[d.ID] != f.stamp {
			f.mark[d.ID] = f.stamp
			f.arena = append(f.arena, d)
		}
	}
	for _, d := range f.soft {
		if gr.read && !gr.circ {
			d.Used = true
		}
		if gr.mutate {
			d.Mutated = true
		}
		if d.isFunc() && (gr.read && !gr.circ || gr.mutate) {
			addUsers(d, f.g, gr, uses, gr.circ)
		}
		add(d)
	}
	for _, d := range f.hard {
		if gr.read {
			d.Used = true
		}
		if gr.mutate {
			d.Mutated = true
		}
		if d.isFunc() {
			addUsers(d, f.g, gr, uses, false)
		}
		add(d)
	}
	if len(f.arena) == start {
		return
	}
	list := f.arena[start:len(f.arena):len(f.arena)]
	slices.SortFunc(list, func(a, b *Def) int { return a.ID - b.ID })
	reads := list
	if f.circularReads && gr.clo != nil && gr.circ && len(f.soft) > 0 {
		// luacheck: the reads in the closure of a circular assignment see
		// only the values it doesn't void
		f.stamp++
		start := len(f.arena)
		for _, d := range f.hard {
			if f.mark[d.ID] != f.stamp {
				f.mark[d.ID] = f.stamp
				f.arena = append(f.arena, d)
			}
		}
		reads = f.arena[start:len(f.arena):len(f.arena)]
		slices.SortFunc(reads, func(a, b *Def) int { return a.ID - b.ID })
	}
	for _, u := range uses {
		if u.Kind == Read {
			u.Defs = reads
		} else {
			u.Defs = list
		}
	}
	if cap(f.arena)-len(f.arena) < 64 {
		f.arena = make([]*Def, 0, slabSize(cap(f.arena), 256, 4096))
	}
}

// addUsers adds to the users of a function value the functions of the
// uses of a group that see it: the variable's function for a node, the
// functions from each use to the closure for a closure; only those of
// mutations with mutOnly.
func addUsers(d *Def, g *Graph, gr *group, uses []*Use, mutOnly bool) {
	if gr.clo == nil {
		d.addUser(g)
		return
	}
	for _, u := range uses {
		if mutOnly && u.Kind != Mutate {
			continue
		}
		for h := u.Node.Block.Graph; h != nil; h = h.Parent {
			d.addUser(h)
			if h == gr.clo {
				break
			}
		}
	}
}

// overwritten finds the assignments that overwrite the values of a
// variable that nothing uses.
func (f *flowState) overwritten(vf *varFlow, defs []*Def) {
	for _, d := range defs {
		if d.Used || d.Mutated || d.Empty {
			continue
		}
		// a later assignment of the variable in the same node
		after := false
		for _, e := range d.Node.Defs {
			if e == d {
				after = true
			} else if after && e.Var == d.Var {
				d.Overwritten = e
				break
			}
		}
		if d.Overwritten != nil || d.Node.Block.Graph != f.g {
			continue
		}
		// (not by its own node, again in a loop: the value is unused)
		if n := f.overwriter(d, vf.decl); n != nil && n != d.Node {
			for _, e := range n.Defs {
				if e.Var == d.Var {
					d.Overwritten = e
					break
				}
			}
		}
	}
}

// overwriter returns the node that every path from a def reaches before
// any other assigning the variable and before the end of its scope, nil if
// there is none.
func (f *flowState) overwriter(d *Def, decl *Def) *Node {
	v := d.Var
	var found *Node
	// the blocks seen, by stamp
	if len(f.seen) < len(f.g.Blocks) {
		f.seen = make([]uint32, len(f.g.Blocks))
		f.seenStamp = 0
	}
	f.seenStamp++
	if f.seenStamp == 0 {
		clear(f.seen)
		f.seenStamp = 1
	}
	seen, stamp := f.seen, f.seenStamp
	type item struct {
		b    *Block
		from int
	}
	stack := []item{{d.Node.Block, d.Node.Index - d.Node.Block.Nodes[0].Index + 1}}
	if d.Node.Kind == Iteration {
		// a loop variable's value is for the body; out of it, the
		// variable is out of scope
		stack = stack[:0]
		for _, e := range d.Node.Block.Succs {
			if e.Kind == True {
				seen[e.To.ID] = stamp
				stack = append(stack, item{e.To, 0})
			}
		}
	}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		stopped := false
		for _, n := range it.b.Nodes[it.from:] {
			if !inScope(n, v, decl) {
				return nil
			}
			if assigns(n, v) {
				if found != nil && found != n {
					return nil
				}
				found = n
				stopped = true
				break
			}
		}
		if stopped {
			continue
		}
		if it.b == f.g.Exit || len(it.b.Succs) == 0 {
			return nil
		}
		for _, e := range it.b.Succs {
			if seen[e.To.ID] != stamp {
				seen[e.To.ID] = stamp
				stack = append(stack, item{e.To, 0})
			}
		}
	}
	return found
}

// lastOfNode reports whether a def is the last of its variable in its node,
// the one that stays.
func lastOfNode(d *Def) bool {
	defs := d.Node.Defs
	for i := len(defs) - 1; i >= 0; i-- {
		if defs[i].Var == d.Var {
			return defs[i] == d
		}
	}
	return true
}

func assigns(n *Node, v *scope.Var) bool {
	for _, d := range n.Defs {
		if d.Var == v {
			return true
		}
	}
	return false
}

// inScope reports whether a node of the variable's function is in its
// scope, after its declaration.
func inScope(n *Node, v *scope.Var, decl *Def) bool {
	if decl != nil && n.Index <= decl.Node.Index {
		return false
	}
	s := n.Scope
	if s == nil {
		return true
	}
	for s != nil && s != v.Scope {
		if s.IsFunction() {
			return false
		}
		s = s.Parent
	}
	return s == v.Scope
}
