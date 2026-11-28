package flow

import (
	"strings"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/scope"
)

// maxDepth is how deep the builder recurses into a tree: the resolver's
// and the compiler's limit.
const maxDepth = 100000

// Options are the options of Analyze. The zero Options are the defaults.
type Options struct {
	// NoReturn names functions that don't return: a global ("error") or a
	// field of a global ("os.exit"). A call statement of one ends its path
	// as a return does (to the Exit, Node.NoReturn), so that what follows
	// it is unreachable and a path ending with it doesn't fall off the end
	// of its function. None by default: calls are opaque, error(...) and
	// os.exit() are calls like any other.
	NoReturn []string

	// luacheckClosures connects, for a local of a function other than the
	// main chunk, every value its closures assign with every use in its
	// closures, as luacheck does (it cross-resolves the closures inside a
	// closure at the levels above the variable's function). For the
	// comparison with luacheck.
	luacheckClosures bool
	// luacheckParens takes a parenthesized function at the end of the
	// values of an assignment as no function (UnusedRecursive), as
	// luacheck, which keeps the parentheses there, does.
	luacheckParens bool
	// luacheckScopes ends the scope of a variable, for the values that
	// reach its uses, at a declaration of the same name in the same scope,
	// as luacheck does: a goto back to before that declaration doesn't
	// take them back into the scope.
	luacheckScopes bool
	// luacheckCircular gives the reads in the functions that a circular
	// assignment creates ("t.f = function() return t end", "function
	// t:m()") none of the values that reach the assignment, as luacheck,
	// which doesn't connect them, does.
	luacheckCircular bool
	// graphsOnly builds the graphs without the data flow (to measure).
	graphsOnly bool
}

// Analyze builds the graphs of the functions of the chunk that info is the
// resolution of (scope.Resolve), and the data flow over their locals. A
// chunk of ResolveStmts is given by AnalyzeStmts.
func Analyze(info *scope.Info, opts *Options) *Program {
	var stmts []ast.Stmt
	if c := info.Chunk(); c != nil {
		stmts = c.Stmts
	}
	return AnalyzeStmts(stmts, info, opts)
}

// AnalyzeStmts is Analyze for the statements that info is the resolution
// of (scope.ResolveStmts).
func AnalyzeStmts(stmts []ast.Stmt, info *scope.Info, opts *Options) *Program {
	p := &Program{Info: info}
	if opts != nil {
		p.luacheckParens = opts.luacheckParens
	}
	b := &builder{p: p, info: info, refs: info.Refs, alloc: &alloc{}}
	if opts != nil && len(opts.NoReturn) > 0 {
		b.noReturn = map[string]bool{}
		for _, n := range opts.NoReturn {
			b.noReturn[n] = true
		}
	}
	if c := info.Chunk(); c != nil && c.Lines != nil {
		b.src = c.Lines.Source()
	}
	if c := info.Chunk(); c != nil {
		// the sizes the archives have: a node per 56 bytes of source, a
		// use per 55, a def per 128, a block per 109
		n := c.Span().End
		b.hint = n
		p.Uses = make([]*Use, 0, n/50+1)
		p.Defs = make([]*Def, 0, n/110+1)
		b.alloc.hint = n
	}
	var root ast.Element
	if c := info.Chunk(); c != nil {
		root = c
	}
	b.function(root, nil, stmts, false)
	b.finish()
	if opts == nil || !opts.graphsOnly {
		p.flow(opts)
	}
	return p
}

// builder builds the graphs of a tree in one pass, in the order the
// resolver resolves it, so that the scopes, variables and occurrences of
// the resolution are met in the order of Info.Scopes, Info.Vars and
// Info.Refs.
type builder struct {
	p        *Program
	info     *scope.Info
	refs     []scope.Ref
	ri       int // the next Ref
	vi       int // the next Var
	si       int // the next Scope
	misses   int // lookups that the order didn't find
	src      []byte
	noReturn map[string]bool

	fn    *fstate
	depth int

	hint     int // the length of the source, for the first slabs
	nodeSlab []Node
	useSlab  []Use
	defSlab  []Def
	alloc    *alloc
	// the nodes of the functions being built, each function's after its
	// parent's; ptrs is what a finished function's are moved to
	nstack []*Node
	ptrs   []*Node
	stack  []*Block
}

// fstate is a function being built.
type fstate struct {
	g    *Graph
	cur  *Block // the block that the next node goes into; nil after a jump
	node *Node  // the node whose expressions are being read
	// slot is the expression of the node being read, for the nodes that
	// evaluate a list of them one after the other (a return's, a for's)
	slot   int32
	scope  *scope.Scope
	loops  []*Block // where a break goes
	labels map[*ast.LabelStmt]*Block
	nbase  int // where its nodes start in builder.nstack
}

/* the resolution, in its order */

func (b *builder) nextRef(id *ast.IdentExpr) *scope.Ref {
	if b.ri < len(b.refs) && b.refs[b.ri].Ident == id {
		r := &b.refs[b.ri]
		b.ri++
		return r
	}
	b.misses++
	return b.info.RefOf(id)
}

func (b *builder) nextJump(st *ast.GotoStmt) *scope.Ref {
	if b.ri < len(b.refs) && b.refs[b.ri].Goto == st && b.refs[b.ri].Kind == scope.Jump {
		r := &b.refs[b.ri]
		b.ri++
		return r
	}
	if b.info.Dialect != scope.LuaJIT {
		return nil // no gotos in Lua51: a problem, no Ref
	}
	b.misses++
	for i := range b.refs {
		if r := &b.refs[i]; r.Goto == st && r.Kind == scope.Jump {
			return r
		}
	}
	return nil
}

func (b *builder) nextVar(decl ast.Element, index int, kind scope.VarKind) *scope.Var {
	vars := b.info.Vars
	if b.vi < len(vars) {
		if v := vars[b.vi]; v.Decl == decl && v.Index == index && v.Kind == kind {
			b.vi++
			return v
		}
	}
	b.misses++
	for i, v := range vars {
		if v.Decl == decl && v.Index == index && v.Kind == kind {
			b.vi = i + 1
			return v
		}
	}
	return nil
}

func (b *builder) nextScope(kind scope.ScopeKind, node ast.Element) *scope.Scope {
	scopes := b.info.Scopes
	if b.si < len(scopes) {
		if s := scopes[b.si]; s.Kind == kind && s.Node == node {
			b.si++
			return s
		}
	}
	b.misses++
	for i := b.si; i < len(scopes); i++ {
		if s := scopes[i]; s.Kind == kind && s.Node == node {
			b.si = i + 1
			return s
		}
	}
	return nil
}

// enter opens a block scope; leave closes it.
func (b *builder) enter(kind scope.ScopeKind, node ast.Element) *scope.Scope {
	outer := b.fn.scope
	if s := b.nextScope(kind, node); s != nil {
		b.fn.scope = s
	}
	return outer
}

func (b *builder) leave(outer *scope.Scope) { b.fn.scope = outer }

/* nodes, uses and defs */

func (b *builder) newNode() *Node {
	if len(b.nodeSlab) == cap(b.nodeSlab) {
		b.nodeSlab = make([]Node, 0, slabSize(cap(b.nodeSlab), b.hint/56+8, 1024))
	}
	b.nodeSlab = b.nodeSlab[:len(b.nodeSlab)+1]
	return &b.nodeSlab[len(b.nodeSlab)-1]
}

// slabSize is the capacity of the next slab: first for the first, then
// twice the last up to limit.
func slabSize(last, first, limit int) int {
	if last == 0 {
		return first
	}
	return max(min(2*last, limit), last)
}

// node adds a node to the current block, or to a new unreachable block
// after a jump.
func (b *builder) node(kind NodeKind, stmt ast.Stmt, expr ast.Expr) *Node {
	fs := b.fn
	g := fs.g
	if fs.cur == nil {
		fs.cur = g.newBlock()
	}
	n := b.newNode()
	n.Kind, n.Stmt, n.Expr, n.Block, n.Scope, n.Index = kind, stmt, expr, fs.cur, fs.scope, len(b.nstack)-fs.nbase
	if fs.cur.first == 0 {
		fs.cur.first = int32(n.Index) + 1
	}
	b.nstack = append(b.nstack, n)
	fs.node = n
	fs.slot = 0
	return n
}

func (b *builder) use(id *ast.IdentExpr, kind UseKind) {
	r := b.nextRef(id)
	if r == nil || r.Var == nil || r.Kind == scope.Jump {
		return
	}
	if len(b.useSlab) == cap(b.useSlab) {
		b.useSlab = make([]Use, 0, slabSize(cap(b.useSlab), b.hint/55+8, 1024))
	}
	b.useSlab = b.useSlab[:len(b.useSlab)+1]
	u := &b.useSlab[len(b.useSlab)-1]
	n := b.fn.node
	u.Var, u.Ident, u.Kind, u.Node, u.ID, u.slot = r.Var, id, kind, n, len(b.p.Uses), b.fn.slot
	b.p.Uses = append(b.p.Uses, u)
	n.nu++
}

func (b *builder) def(v *scope.Var, name *ast.IdentExpr, value ast.Expr, init, empty bool) {
	if v == nil {
		return
	}
	if len(b.defSlab) == cap(b.defSlab) {
		b.defSlab = make([]Def, 0, slabSize(cap(b.defSlab), b.hint/128+8, 1024))
	}
	b.defSlab = b.defSlab[:len(b.defSlab)+1]
	d := &b.defSlab[len(b.defSlab)-1]
	n := b.fn.node
	d.Var, d.Node, d.Name, d.Value, d.Init, d.Empty, d.ID = v, n, name, value, init, empty, len(b.p.Defs)
	b.p.Defs = append(b.p.Defs, d)
	n.nd++
}

/* functions */

// function builds the graph of a function: the main chunk (fe nil, node
// the chunk or nil) or a FunctionExpr; method gives it a self.
func (b *builder) function(node ast.Element, fe *ast.FunctionExpr, stmts []ast.Stmt, method bool) {
	parent := b.fn
	g := &Graph{Function: fe, ID: len(b.p.Graphs)}
	b.p.Graphs = append(b.p.Graphs, g)
	if parent != nil {
		g.Parent, g.Creator, g.slot = parent.g, parent.node, parent.slot
		parent.node.Closures = append(parent.node.Closures, g)
	}
	g.alloc = b.alloc
	fs := &fstate{g: g, nbase: len(b.nstack)}
	b.fn = fs
	if fe != nil {
		node = fe
	}
	g.Func = b.nextScope(scope.ScopeFunction, node)
	fs.scope = g.Func
	g.Entry = g.newBlock()
	g.Exit = g.newBlock()
	fs.cur = g.Entry
	b.node(Enter, nil, nil)
	if fe != nil {
		if method {
			b.def(b.nextVar(fe, -1, scope.KindSelf), nil, nil, true, false)
		}
		if pl := fe.ParList; pl != nil {
			for i := range pl.Names {
				b.def(b.nextVar(fe, i, scope.KindParam), nil, nil, true, false)
			}
			if pl.HasVargs && b.info.Dialect == scope.Lua51 && !pl.NoArg {
				b.def(b.nextVar(fe, -1, scope.KindArg), nil, nil, true, false)
			}
		}
	}
	body := g.newBlock()
	link(g.Entry, body, Next)
	fs.cur = body
	if fe != nil && len(fe.Stmts) == 0 {
		b.p.Empty = append(b.p.Empty, Empty{Kind: EmptyFunction, Function: fe, Span: fe.StmtsSpan})
	}
	b.stmts(stmts)
	if fs.cur != nil {
		link(fs.cur, g.Exit, Next)
		g.fallOff = fs.cur
	}
	b.finishGraph(fs)
	b.fn = parent
}

// finishGraph moves the graph's nodes off the stack, carves the blocks'
// nodes and predecessors, and marks the reachable blocks.
func (b *builder) finishGraph(fs *fstate) {
	g := fs.g
	n := len(b.nstack) - fs.nbase
	if cap(b.ptrs)-len(b.ptrs) < n {
		b.ptrs = make([]*Node, 0, max(n, slabSize(cap(b.ptrs), b.hint/56+16, 4096)))
	}
	g.Nodes = b.ptrs[len(b.ptrs) : len(b.ptrs)+n : len(b.ptrs)+n]
	b.ptrs = b.ptrs[:len(b.ptrs)+n]
	copy(g.Nodes, b.nstack[fs.nbase:])
	clear(b.nstack[fs.nbase:])
	b.nstack = b.nstack[:fs.nbase]
	// a block's nodes are consecutive: a block takes nodes only while it
	// is the current one, and is current once
	np := 0
	for _, blk := range g.Blocks {
		if blk.first > 0 {
			lo := int(blk.first) - 1
			hi := lo
			for hi < len(g.Nodes) && g.Nodes[hi].Block == blk {
				hi++
			}
			blk.Nodes = g.Nodes[lo:hi:hi]
		}
		for _, e := range blk.Succs {
			e.To.npred++
			np++
		}
	}
	preds := make([]*Block, np)
	for _, blk := range g.Blocks {
		blk.Preds = preds[:0:blk.npred]
		preds = preds[blk.npred:]
	}
	for _, blk := range g.Blocks {
		for _, e := range blk.Succs {
			e.To.Preds = append(e.To.Preds, blk)
		}
	}
	stack := append(b.stack[:0], g.Entry)
	g.Entry.Reachable = true
	for len(stack) > 0 {
		blk := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range blk.Succs {
			if !e.To.Reachable {
				e.To.Reachable = true
				stack = append(stack, e.To)
			}
		}
	}
	b.stack = stack
}

// finish carves the defs and uses of each node and each variable.
func (b *builder) finish() {
	p := b.p
	ub := make([]*Use, len(p.Uses))
	db := make([]*Def, len(p.Defs))
	uo, do := 0, 0
	for _, g := range p.Graphs {
		for _, n := range g.Nodes {
			nu, nd := int(n.nu), int(n.nd)
			n.Uses = ub[uo : uo : uo+nu]
			n.Defs = db[do : do : do+nd]
			uo += nu
			do += nd
		}
	}
	for _, u := range p.Uses {
		u.Node.Uses = append(u.Node.Uses, u)
	}
	for _, d := range p.Defs {
		d.Node.Defs = append(d.Node.Defs, d)
	}
	nv := len(b.info.Vars)
	cu := make([]int32, nv+1)
	cd := make([]int32, nv+1)
	for _, u := range p.Uses {
		cu[u.Var.ID]++
	}
	for _, d := range p.Defs {
		cd[d.Var.ID]++
	}
	p.varUses = make([][]*Use, nv)
	p.varDefs = make([][]*Def, nv)
	ub2 := make([]*Use, len(p.Uses))
	db2 := make([]*Def, len(p.Defs))
	uo, do = 0, 0
	for i := 0; i < nv; i++ {
		p.varUses[i] = ub2[uo : uo : uo+int(cu[i])]
		p.varDefs[i] = db2[do : do : do+int(cd[i])]
		uo += int(cu[i])
		do += int(cd[i])
	}
	for _, u := range p.Uses {
		p.varUses[u.Var.ID] = append(p.varUses[u.Var.ID], u)
	}
	for _, d := range p.Defs {
		p.varDefs[d.Var.ID] = append(p.varDefs[d.Var.ID], d)
	}
	p.graphOf = make([]*Graph, len(b.info.Scopes))
	for _, g := range p.Graphs {
		if g.Func != nil && g.Func.ID < len(p.graphOf) {
			p.graphOf[g.Func.ID] = g
		}
	}
	p.misses = b.misses
}

/* statements */

func (b *builder) stmts(list []ast.Stmt) {
	for _, s := range list {
		b.stmt(s)
	}
}

func (b *builder) stmt(s ast.Stmt) {
	if b.depth >= maxDepth {
		return
	}
	b.depth++
	b.stmt1(s)
	b.depth--
}

// block builds a block with a scope of its own.
func (b *builder) block(kind scope.ScopeKind, node ast.Element, list []ast.Stmt) {
	outer := b.enter(kind, node)
	b.stmts(list)
	b.leave(outer)
}

func (b *builder) stmt1(s ast.Stmt) {
	fs := b.fn
	switch st := s.(type) {
	case *ast.AssignStmt:
		if st != nil {
			b.assign(st)
		}
	case *ast.LocalAssignStmt:
		if st != nil {
			b.localAssign(st)
		}
	case *ast.FuncCallStmt:
		if st == nil {
			return
		}
		b.node(Statement, st, nil)
		noReturn := b.isNoReturn(st)
		b.expr(st.Expr)
		if noReturn {
			fs.node.NoReturn = true
			link(fs.cur, fs.g.Exit, Next)
			fs.cur = nil
		}
	case *ast.DoBlockStmt:
		if st == nil {
			return
		}
		b.node(Statement, st, nil)
		if len(st.Stmts) == 0 {
			b.empty(EmptyDo, st, st.StmtsSpan)
		}
		b.block(scope.ScopeDo, st, st.Stmts)
	case *ast.WhileStmt:
		if st != nil {
			b.while(st)
		}
	case *ast.RepeatStmt:
		if st != nil {
			b.repeat(st)
		}
	case *ast.IfStmt:
		if st != nil {
			b.ifStmt(st)
		}
	case *ast.NumberForStmt:
		if st == nil {
			return
		}
		v := b.nextVar(st, 0, scope.KindForNum)
		b.node(Statement, st, nil)
		b.expr(st.Init)
		fs.slot = 1
		b.expr(st.Limit)
		fs.slot = 2
		b.expr(st.Step)
		b.forLoop(st, st.Stmts, st.StmtsSpan, []*scope.Var{v})
	case *ast.GenericForStmt:
		if st == nil {
			return
		}
		var buf [4]*scope.Var
		vars := buf[:0]
		for i := range st.Names {
			vars = append(vars, b.nextVar(st, i, scope.KindForIn))
		}
		b.node(Statement, st, nil)
		b.slots(st.Exprs)
		b.forLoop(st, st.Stmts, st.StmtsSpan, vars)
	case *ast.FuncDefStmt:
		if st != nil {
			b.funcDef(st)
		}
	case *ast.ReturnStmt:
		if st == nil {
			return
		}
		b.node(Statement, st, nil)
		b.slots(st.Exprs)
		link(fs.cur, fs.g.Exit, Next)
		fs.cur = nil
	case *ast.BreakStmt:
		if st == nil {
			return
		}
		b.node(Statement, st, nil)
		if len(fs.loops) > 0 {
			link(fs.cur, fs.loops[len(fs.loops)-1], Next)
		}
		fs.cur = nil
	case *ast.GotoStmt:
		if st == nil {
			return
		}
		b.node(Statement, st, nil)
		if r := b.nextJump(st); r != nil && r.Var != nil {
			if l, ok := r.Var.Decl.(*ast.LabelStmt); ok && l != nil {
				link(fs.cur, b.labelBlock(l), Next)
			}
		}
		fs.cur = nil
	case *ast.LabelStmt:
		if st == nil {
			return
		}
		lb := b.labelBlock(st)
		if fs.cur != nil {
			link(fs.cur, lb, Next)
		}
		fs.cur = lb
		b.node(Statement, st, nil)
		if b.info.Dialect == scope.LuaJIT {
			b.nextVar(st, 0, scope.KindLabel)
		}
	}
}

func (b *builder) labelBlock(l *ast.LabelStmt) *Block {
	fs := b.fn
	if fs.labels == nil {
		fs.labels = map[*ast.LabelStmt]*Block{}
	}
	lb := fs.labels[l]
	if lb == nil {
		lb = fs.g.newBlock()
		fs.labels[l] = lb
	}
	return lb
}

// isNoReturn reports whether a call statement calls a function of
// Options.NoReturn. The name of the called global is the next Ref.
func (b *builder) isNoReturn(st *ast.FuncCallStmt) bool {
	if b.noReturn == nil {
		return false
	}
	call, ok := st.Expr.(*ast.FuncCallExpr)
	if !ok || call == nil || call.Func == nil {
		return false
	}
	var id *ast.IdentExpr
	name := ""
	switch f := call.Func.(type) {
	case *ast.IdentExpr:
		id, name = f, f.Value
	case *ast.AttrGetExpr:
		obj, ok1 := f.Object.(*ast.IdentExpr)
		key, ok2 := f.Key.(*ast.StringExpr)
		if !ok1 || !ok2 || obj == nil || key == nil {
			return false
		}
		id, name = obj, obj.Value+"."+key.Value
	default:
		return false
	}
	if id == nil || !b.noReturn[name] {
		return false
	}
	if b.ri < len(b.refs) && b.refs[b.ri].Ident == id {
		return b.refs[b.ri].Var == nil
	}
	r := b.info.RefOf(id)
	return r != nil && r.Var == nil
}

func (b *builder) assign(st *ast.AssignStmt) {
	b.node(Statement, st, nil)
	n := b.fn.node
	u0 := len(b.p.Uses)
	var buf [4]*scope.Var
	targets := buf[:0]
	for _, e := range st.Lhs {
		var v *scope.Var
		switch x := e.(type) {
		case *ast.IdentExpr:
			if x != nil {
				if r := b.nextRef(x); r != nil {
					v = r.Var
				}
			}
		case *ast.AttrGetExpr:
			if x != nil {
				b.indexTarget(x)
			}
		default:
			b.expr(e)
		}
		targets = append(targets, v)
	}
	b.exprs(st.Rhs)
	for i, v := range targets {
		if v != nil {
			b.def(v, st.Lhs[i].(*ast.IdentExpr), valueAt(st.Rhs, i, len(st.Lhs)), false, false)
		}
	}
	// "t.k = e", one target and one value without calls: reading t in it
	// doesn't use t's value (luacheck's circular reference)
	if len(st.Lhs) == 1 && len(st.Rhs) == 1 {
		if x, ok := st.Lhs[0].(*ast.AttrGetExpr); ok && x != nil {
			if id, ok := x.Object.(*ast.IdentExpr); ok && id != nil && !hasCall(x) && !hasCall(st.Rhs[0]) {
				for _, u := range b.p.Uses[u0:] {
					if u.Ident == id && u.Node == n {
						n.circular = u.Var
					}
				}
			}
		}
	}
}

// indexTarget reads an indexed assignment target: the name the chain of
// indexes starts from is mutated, the keys are read.
func (b *builder) indexTarget(x *ast.AttrGetExpr) {
	switch o := x.Object.(type) {
	case *ast.IdentExpr:
		if o != nil {
			b.use(o, Mutate)
		}
	case *ast.AttrGetExpr:
		if o != nil {
			b.indexTarget(o)
		}
	default:
		b.expr(x.Object)
	}
	b.expr(x.Key)
}

// valueAt returns the value that the i-th of n names gets from a list of
// expressions: its expression, the call or "..." at the end of the list
// that it is unpacked from, or nil.
func valueAt(list []ast.Expr, i, n int) ast.Expr {
	if i < len(list) {
		return list[i]
	}
	if len(list) > 0 && n > len(list) {
		switch x := list[len(list)-1].(type) {
		case *ast.FuncCallExpr:
			if x != nil && !x.AdjustRet && x.Parens().Count == 0 {
				return x
			}
		case *ast.Comma3Expr:
			if x != nil && !x.AdjustRet && x.Parens().Count == 0 {
				return x
			}
		}
	}
	return nil
}

func (b *builder) localAssign(st *ast.LocalAssignStmt) {
	var buf [4]*scope.Var
	vars := buf[:0]
	fn := st.IsFunction && len(st.Names) == 1 && len(st.Exprs) == 1
	if fn {
		_, fn = st.Exprs[0].(*ast.FunctionExpr)
	}
	kind := scope.KindLocal
	if fn {
		kind = scope.KindLocalFunction
	}
	for i := range st.Names {
		vars = append(vars, b.nextVar(st, i, kind))
	}
	b.node(Statement, st, nil)
	b.exprs(st.Exprs)
	for i, v := range vars {
		val := valueAt(st.Exprs, i, len(st.Names))
		b.def(v, nil, val, true, val == nil)
	}
}

func (b *builder) funcDef(st *ast.FuncDefStmt) {
	b.node(Statement, st, nil)
	if st.Name == nil {
		b.function(nil, st.Func, funcStmts(st.Func), false)
		return
	}
	var v *scope.Var
	var name *ast.IdentExpr
	method := false
	switch {
	case st.Name.Func != nil:
		switch x := st.Name.Func.(type) {
		case *ast.IdentExpr:
			if x != nil {
				if r := b.nextRef(x); r != nil {
					v, name = r.Var, x
				}
			}
		case *ast.AttrGetExpr:
			if x != nil {
				b.indexTarget(x)
			}
		default:
			b.expr(st.Name.Func)
		}
	case st.Name.Receiver != nil:
		switch x := st.Name.Receiver.(type) {
		case *ast.IdentExpr:
			if x != nil {
				b.use(x, Mutate)
			}
		case *ast.AttrGetExpr:
			if x != nil {
				b.indexTarget(x)
			}
		default:
			b.expr(st.Name.Receiver)
		}
		method = true
	}
	// "function t.f()" and "function t:m()" assign t.f and t.m: circular
	// assignments of t, as "t.f = function() end" is
	var base *ast.IdentExpr
	if x, ok := st.Name.Func.(*ast.AttrGetExpr); ok && x != nil {
		base, _ = x.Object.(*ast.IdentExpr)
	} else if st.Name.Func == nil {
		base, _ = st.Name.Receiver.(*ast.IdentExpr)
	}
	if base != nil {
		n := b.fn.node
		for i := len(b.p.Uses) - 1; i >= 0 && b.p.Uses[i].Node == n; i-- {
			if u := b.p.Uses[i]; u.Ident == base {
				n.circular = u.Var
			}
		}
	}
	if st.Func != nil {
		b.function(nil, st.Func, st.Func.Stmts, method)
	}
	if v != nil {
		b.def(v, name, st.Func, false, false)
	}
}

func funcStmts(fe *ast.FunctionExpr) []ast.Stmt {
	if fe == nil {
		return nil
	}
	return fe.Stmts
}

// constTruth returns the truth of a condition that luacheck takes as a
// constant: a literal (nil, false, true, a number, a string) or a table
// or function constructor.
func constTruth(e ast.Expr) (truth, known bool) {
	switch e.(type) {
	case *ast.NilExpr, *ast.FalseExpr:
		return false, true
	case *ast.TrueExpr, *ast.NumberExpr, *ast.StringExpr, *ast.CdataNumberExpr, *ast.TableExpr, *ast.FunctionExpr:
		return true, true
	}
	return false, false
}

// branch links a block ending with a condition to where it holds and to
// where it does not; a constant condition has one of the edges.
func branch(from *Block, cond ast.Expr, yes, no *Block) {
	truth, known := constTruth(cond)
	if !known || truth {
		link(from, yes, True)
	}
	if !known || !truth {
		link(from, no, False)
	}
}

func (b *builder) while(st *ast.WhileStmt) {
	fs := b.fn
	b.node(Statement, st, nil)
	head := fs.g.newBlock()
	link(fs.cur, head, Next)
	fs.cur = head
	b.node(Condition, st, st.Condition)
	b.expr(st.Condition)
	body, after := fs.g.newBlock(), fs.g.newBlock()
	branch(fs.cur, st.Condition, body, after)
	if len(st.Stmts) == 0 {
		b.empty(EmptyLoop, st, st.StmtsSpan)
	}
	fs.cur = body
	fs.loops = append(fs.loops, after)
	b.block(scope.ScopeWhile, st, st.Stmts)
	fs.loops = fs.loops[:len(fs.loops)-1]
	b.node(LoopEnd, st, nil)
	link(fs.cur, head, Next)
	fs.cur = after
}

func (b *builder) repeat(st *ast.RepeatStmt) {
	fs := b.fn
	b.node(Statement, st, nil)
	body := fs.g.newBlock()
	link(fs.cur, body, Next)
	after := fs.g.newBlock()
	if len(st.Stmts) == 0 {
		b.empty(EmptyLoop, st, st.StmtsSpan)
	}
	fs.cur = body
	fs.loops = append(fs.loops, after)
	outer := b.enter(scope.ScopeRepeat, st)
	b.stmts(st.Stmts)
	b.node(Condition, st, st.Condition) // in the body's scope
	b.expr(st.Condition)
	b.leave(outer)
	fs.loops = fs.loops[:len(fs.loops)-1]
	branch(fs.cur, st.Condition, after, body)
	fs.cur = after
}

// forLoop builds a for loop after its Statement node: the Iteration that
// gives the variables their values, the body, the LoopEnd.
func (b *builder) forLoop(st ast.Stmt, stmts []ast.Stmt, span ast.Span, vars []*scope.Var) {
	fs := b.fn
	head := fs.g.newBlock()
	link(fs.cur, head, Next)
	fs.cur = head
	outer := b.enter(scope.ScopeFor, st)
	b.node(Iteration, st, nil)
	for _, v := range vars {
		b.def(v, nil, nil, true, false)
	}
	body, after := fs.g.newBlock(), fs.g.newBlock()
	link(head, body, True)
	link(head, after, False)
	if len(stmts) == 0 {
		b.empty(EmptyLoop, st, span)
	}
	fs.cur = body
	fs.loops = append(fs.loops, after)
	b.block(scope.ScopeForBody, st, stmts)
	fs.loops = fs.loops[:len(fs.loops)-1]
	b.leave(outer)
	b.node(LoopEnd, st, nil)
	link(fs.cur, head, Next)
	fs.cur = after
}

// isElseif reports whether an IfStmt alone in an else block is an
// "elseif" rather than an "if" statement in the block ("else if"), from
// the source; without one, it is taken as an elseif.
func (b *builder) isElseif(ei *ast.IfStmt) bool {
	sp := ei.Span()
	if b.src == nil || sp.End <= sp.Start || sp.End > len(b.src) {
		return true
	}
	return strings.HasPrefix(string(b.src[sp.Start:min(sp.End, sp.Start+6)]), "elseif")
}

func (b *builder) ifStmt(st *ast.IfStmt) {
	fs := b.fn
	b.node(Statement, st, nil)
	join := fs.g.newBlock()
	for clause := st; ; {
		b.node(Condition, clause, clause.Condition)
		b.expr(clause.Condition)
		from := fs.cur
		then := fs.g.newBlock()
		var next *ast.IfStmt
		if len(clause.Else) == 1 {
			if ei, ok := clause.Else[0].(*ast.IfStmt); ok && ei != nil {
				next = ei
			}
		}
		no := join
		if len(clause.Else) > 0 {
			no = fs.g.newBlock()
		}
		branch(from, clause.Condition, then, no)
		if len(clause.Then) == 0 {
			b.empty(EmptyThen, clause, clause.ThenSpan)
		}
		fs.cur = then
		b.block(scope.ScopeThen, clause, clause.Then)
		if fs.cur != nil {
			link(fs.cur, join, Next)
		}
		fs.cur = no
		if next != nil {
			if !b.isElseif(next) { // "else if": an if statement of its own
				b.node(Statement, next, nil)
			}
			clause = next
			continue
		}
		switch {
		case len(clause.Else) > 0:
			b.block(scope.ScopeElse, clause, clause.Else)
			if fs.cur != nil {
				link(fs.cur, join, Next)
			}
		case clause.ElseSpan != (ast.Span{}):
			b.empty(EmptyElse, clause, clause.ElseSpan)
		}
		break
	}
	fs.cur = join
}

/* expressions */

// slots reads a list of expressions that are evaluated one after the
// other, each in a slot of its own.
func (b *builder) slots(list []ast.Expr) {
	for i, e := range list {
		b.fn.slot = int32(i)
		b.expr(e)
	}
}

func (b *builder) exprs(list []ast.Expr) {
	for _, e := range list {
		b.expr(e)
	}
}

func (b *builder) expr(e ast.Expr) {
	if b.depth >= maxDepth {
		return
	}
	b.depth++
	b.expr1(e)
	b.depth--
}

func (b *builder) expr1(e ast.Expr) {
	switch x := e.(type) {
	case *ast.IdentExpr:
		if x != nil {
			b.use(x, Read)
		}
	case *ast.AttrGetExpr:
		if x != nil {
			b.expr(x.Object)
			b.expr(x.Key)
		}
	case *ast.FuncCallExpr:
		if x != nil {
			if x.Func != nil {
				b.expr(x.Func)
			} else {
				b.expr(x.Receiver)
			}
			b.exprs(x.Args)
		}
	case *ast.TableExpr:
		if x != nil {
			for _, f := range x.Fields {
				if f != nil {
					b.expr(f.Key)
					b.expr(f.Value)
				}
			}
		}
	case *ast.LogicalOpExpr:
		if x != nil {
			b.expr(x.Lhs)
			b.expr(x.Rhs)
		}
	case *ast.RelationalOpExpr:
		if x != nil {
			b.expr(x.Lhs)
			b.expr(x.Rhs)
		}
	case *ast.ArithmeticOpExpr:
		if x != nil {
			b.expr(x.Lhs)
			b.expr(x.Rhs)
		}
	case *ast.StringConcatOpExpr:
		if x != nil {
			b.expr(x.Lhs)
			b.expr(x.Rhs)
		}
	case *ast.UnaryMinusOpExpr:
		if x != nil {
			b.expr(x.Expr)
		}
	case *ast.UnaryNotOpExpr:
		if x != nil {
			b.expr(x.Expr)
		}
	case *ast.UnaryLenOpExpr:
		if x != nil {
			b.expr(x.Expr)
		}
	case *ast.FunctionExpr:
		if x != nil {
			b.function(nil, x, x.Stmts, false)
		}
	}
}

// hasCall reports whether an expression has a call, outside the functions
// in it.
func hasCall(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.FuncCallExpr:
		return x != nil
	case *ast.AttrGetExpr:
		return x != nil && (hasCall(x.Object) || hasCall(x.Key))
	case *ast.TableExpr:
		if x != nil {
			for _, f := range x.Fields {
				if f != nil && (hasCall(f.Key) || hasCall(f.Value)) {
					return true
				}
			}
		}
	case *ast.LogicalOpExpr:
		return x != nil && (hasCall(x.Lhs) || hasCall(x.Rhs))
	case *ast.RelationalOpExpr:
		return x != nil && (hasCall(x.Lhs) || hasCall(x.Rhs))
	case *ast.ArithmeticOpExpr:
		return x != nil && (hasCall(x.Lhs) || hasCall(x.Rhs))
	case *ast.StringConcatOpExpr:
		return x != nil && (hasCall(x.Lhs) || hasCall(x.Rhs))
	case *ast.UnaryMinusOpExpr:
		return x != nil && hasCall(x.Expr)
	case *ast.UnaryNotOpExpr:
		return x != nil && hasCall(x.Expr)
	case *ast.UnaryLenOpExpr:
		return x != nil && hasCall(x.Expr)
	}
	return false
}

func (b *builder) empty(kind EmptyKind, st ast.Stmt, span ast.Span) {
	b.p.Empty = append(b.p.Empty, Empty{Kind: kind, Stmt: st, Span: span})
}
