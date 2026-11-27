package scope

import (
	"fmt"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
)

// maxDepth is how deep Resolve recurses into a tree: the compiler's limit
// (maxCompileDepth), which also stops a tree with a cycle.
const maxDepth = 100000

// Resolve resolves the names of a chunk. The tree is not modified. It
// returns the Info of what it resolved, and an *Error if the tree has
// problems with its labels (a parsed tree has none).
func Resolve(chunk *ast.Chunk, dialect Dialect) (*Info, error) {
	if chunk == nil {
		return ResolveStmts(nil, dialect)
	}
	return resolve(chunk, chunk.Stmts, dialect)
}

// ResolveStmts resolves the names of the statements of a chunk, a tree
// that the parser built or other code did. The root scope has no node and
// no span.
func ResolveStmts(stmts []ast.Stmt, dialect Dialect) (*Info, error) {
	return resolve(nil, stmts, dialect)
}

func resolve(chunk *ast.Chunk, stmts []ast.Stmt, dialect Dialect) (*Info, error) {
	in := &Info{Dialect: dialect, chunk: chunk}
	r := &resolver{info: in, dialect: dialect}
	var node ast.Element
	var span ast.Span
	if chunk != nil {
		node, span = chunk, chunk.Span()
		// the sizes the archives have: a name occurrence per 42 bytes of
		// source, a variable per 144, a scope per 250
		n := span.End
		in.Refs = make([]Ref, 0, n/36+1)
		in.Vars = make([]*Var, 0, n/140+1)
		in.Scopes = make([]*Scope, 0, n/240+1)
		r.varHint, r.scopeHint = n/140+1, n/240+1
	}
	r.act = make([]*Var, 0, 32)
	root := r.openFunc(node, span)
	r.stmtList(stmts, true)
	r.closeFunc(root)
	in.Root = root.scope
	r.finish()
	if len(in.Problems) > 0 {
		return in, &Error{Problems: in.Problems}
	}
	return in, nil
}

// resolver is the state of a resolution: the active variables, the
// function and the blocks being resolved, the labels and gotos.
type resolver struct {
	info    *Info
	dialect Dialect

	act    []*Var // the active variables of all enclosing functions
	fn     *funcState
	cur    *Scope
	blocks []blockState
	gola   []golaEntry

	depth   int
	aborted bool

	varSlab   []Var
	scopeSlab []Scope
	// the capacity of the first slab of each
	varHint, scopeHint int
}

// funcState is a function being resolved.
type funcState struct {
	parent    *funcState
	scope     *Scope
	actBase   int // len(act) at its start
	golaBase  int
	blockBase int // len(blocks) at its start
}

// blockState is a block of the function being resolved (lj_parse.c's
// FuncScope, for the labels).
type blockState struct {
	nact    int // the function's active variables at its start
	vstart  int // where its gotos and labels start in gola
	hasGola bool
}

// golaEntry is a pending goto or a label (lj_parse.c's VarInfo).
type golaEntry struct {
	name    string
	isLabel bool
	valid   bool
	slot    int  // the function's active variables there
	ref     int  // for a goto, its index in Info.Refs
	label   *Var // for a label
}

// slabSize is the capacity of the next slab: hint for the first, then
// twice the last up to limit.
func slabSize(last, hint, limit int) int {
	if last == 0 {
		return max(hint, 8)
	}
	return min(2*last, limit)
}

func (r *resolver) newVar() *Var {
	if len(r.varSlab) == cap(r.varSlab) {
		r.varSlab = make([]Var, 0, slabSize(cap(r.varSlab), r.varHint, 1024))
	}
	r.varSlab = r.varSlab[:len(r.varSlab)+1]
	v := &r.varSlab[len(r.varSlab)-1]
	v.ID = len(r.info.Vars)
	r.info.Vars = append(r.info.Vars, v)
	return v
}

func (r *resolver) newScope(kind ScopeKind, node ast.Element, span ast.Span) *Scope {
	if len(r.scopeSlab) == cap(r.scopeSlab) {
		r.scopeSlab = make([]Scope, 0, slabSize(cap(r.scopeSlab), r.scopeHint, 512))
	}
	r.scopeSlab = r.scopeSlab[:len(r.scopeSlab)+1]
	s := &r.scopeSlab[len(r.scopeSlab)-1]
	s.Kind, s.Node, s.Span, s.Parent = kind, node, span, r.cur
	s.ID = len(r.info.Scopes)
	s.Func = s
	if r.cur != nil {
		s.Func = r.cur.Func
	}
	r.info.Scopes = append(r.info.Scopes, s)
	return s
}

// declare makes a variable, not yet active.
func (r *resolver) declare(name string, kind VarKind, decl ast.Element, index int, span ast.Span) *Var {
	v := r.newVar()
	v.Name, v.Kind, v.Decl, v.Index, v.NameSpan = name, kind, decl, index, span
	return v
}

// activate makes a declared variable visible, in the current scope.
func (r *resolver) activate(v *Var) {
	v.Scope = r.cur
	v.Shadows = r.lookup(v.Name)
	r.act = append(r.act, v)
}

// lookup finds the variable a name refers to, nil for a global.
func (r *resolver) lookup(name string) *Var {
	act := r.act
	for i := len(act) - 1; i >= 0; i-- {
		if v := act[i]; v.Name == name {
			return v
		}
	}
	return nil
}

func (r *resolver) nact() int { return len(r.act) - r.fn.actBase }

func (r *resolver) problem(node ast.Element, span ast.Span, format string, args ...any) {
	r.info.Problems = append(r.info.Problems, Problem{Node: node, Span: span, Msg: fmt.Sprintf(format, args...)})
}

// ref records an occurrence of a name.
func (r *resolver) ref(id *ast.IdentExpr, kind RefKind) {
	v := r.lookup(id.Value)
	r.info.Refs = append(r.info.Refs, Ref{Kind: kind, Ident: id, Var: v, Scope: r.cur})
	if v != nil {
		if kind == Write {
			v.nw++
		} else {
			v.nr++
		}
		if v.Scope.Func != r.cur.Func {
			r.capture(v)
		}
	}
}

// capture makes v an upvalue of the current function and of those between
// it and the function declaring v.
func (r *resolver) capture(v *Var) {
	v.Captured = true
	for f := r.fn; f != nil && f.scope != v.Scope.Func; f = f.parent {
		s := f.scope
		found := false
		for _, u := range s.Upvalues {
			if u == v {
				found = true
				break
			}
		}
		if !found {
			s.Upvalues = append(s.Upvalues, v)
		}
	}
}

/* functions and blocks */

// openFunc starts a function: its scope and its outermost block.
func (r *resolver) openFunc(node ast.Element, span ast.Span) *funcState {
	fs := &funcState{parent: r.fn, actBase: len(r.act), golaBase: len(r.gola), blockBase: len(r.blocks)}
	s := r.newScope(ScopeFunction, node, span)
	s.Func = s
	fs.scope = s
	r.cur = s
	r.fn = fs
	r.blocks = append(r.blocks, blockState{vstart: len(r.gola)})
	return fs
}

func (r *resolver) closeFunc(fs *funcState) {
	r.leaveBlock()
	r.gola = r.gola[:fs.golaBase]
	r.blocks = r.blocks[:fs.blockBase]
	r.act = r.act[:fs.actBase]
	r.cur = fs.scope.Parent
	r.fn = fs.parent
}

// openScope starts a block with a scope of its own.
func (r *resolver) openScope(kind ScopeKind, node ast.Element, span ast.Span) *Scope {
	s := r.newScope(kind, node, span)
	r.cur = s
	r.blocks = append(r.blocks, blockState{nact: r.nact(), vstart: len(r.gola)})
	return s
}

func (r *resolver) closeScope() {
	r.leaveBlock()
	r.cur = r.cur.Parent
}

// leaveBlock ends the current block: its variables go out of scope, its
// labels are resolved and its gotos passed on (fscope_end).
func (r *resolver) leaveBlock() {
	i := len(r.blocks) - 1
	bl := r.blocks[i]
	r.act = r.act[:r.fn.actBase+bl.nact]
	if bl.hasGola {
		r.golaFixup(&bl, i > r.fn.blockBase)
	}
	r.blocks = r.blocks[:i]
}

// golaFixup resolves the gotos back to the labels of the block going out
// of scope and passes the others on to the enclosing block, if there is
// one in the function; it is an error in the function's (gola_fixup).
func (r *resolver) golaFixup(bl *blockState, hasParent bool) {
	for i := bl.vstart; i < len(r.gola); i++ {
		v := &r.gola[i]
		if !v.valid {
			continue
		}
		switch {
		case v.isLabel:
			v.valid = false
			for j := i + 1; j < len(r.gola); j++ {
				if g := &r.gola[j]; g.valid && !g.isLabel && g.name == v.name {
					r.bind(g, v.label)
				}
			}
		case hasParent:
			r.blocks[len(r.blocks)-2].hasGola = true
			v.slot = bl.nact
		default:
			v.valid = false
			ref := &r.info.Refs[v.ref]
			r.problem(ref.Goto, ref.Span(), "undefined label '%s'", v.name)
		}
	}
}

// bind makes a goto jump to a label.
func (r *resolver) bind(g *golaEntry, label *Var) {
	g.valid = false
	ref := &r.info.Refs[g.ref]
	ref.Var = label
	label.nr++
}

/* statements */

// stmtList resolves the statements of a block; trailing says whether the
// labels at its end are out of its locals' scope (it is not for the body
// of a repeat, which "until" follows).
func (r *resolver) stmtList(list []ast.Stmt, trailing bool) {
	// the labels from tail on are at the end of the block
	tail := len(list)
	if r.dialect == LuaJIT && trailing {
		for tail > 0 {
			if l, ok := list[tail-1].(*ast.LabelStmt); !ok || l == nil {
				break
			}
			tail--
		}
	} else {
		tail = len(list) + 1
	}
	for i, s := range list {
		if r.aborted {
			return
		}
		if l, ok := s.(*ast.LabelStmt); ok && l != nil {
			r.label(l, i >= tail)
			continue
		}
		r.stmt(s)
	}
}

// block resolves a block with a scope of its own.
func (r *resolver) block(kind ScopeKind, node ast.Element, span ast.Span, list []ast.Stmt) {
	r.openScope(kind, node, span)
	r.stmtList(list, true)
	r.closeScope()
}

func (r *resolver) stmt(s ast.Stmt) {
	if r.depth >= maxDepth {
		r.tooDeep(s)
		return
	}
	r.depth++
	r.stmt1(s)
	r.depth--
}

func (r *resolver) tooDeep(n ast.Element) {
	if !r.aborted {
		r.aborted = true
		r.problem(n, ast.Span{}, "syntax tree too deep")
	}
}

func (r *resolver) stmt1(s ast.Stmt) {
	switch st := s.(type) {
	case *ast.AssignStmt:
		if st == nil {
			return
		}
		for _, e := range st.Lhs {
			if id, ok := e.(*ast.IdentExpr); ok && id != nil {
				r.ref(id, Write)
			} else {
				r.expr(e)
			}
		}
		r.exprs(st.Rhs)
	case *ast.LocalAssignStmt:
		if st == nil {
			return
		}
		r.localAssign(st)
	case *ast.FuncCallStmt:
		if st == nil {
			return
		}
		r.expr(st.Expr)
	case *ast.DoBlockStmt:
		if st == nil {
			return
		}
		r.block(ScopeDo, st, st.StmtsSpan, st.Stmts)
	case *ast.WhileStmt:
		if st == nil {
			return
		}
		r.expr(st.Condition)
		r.block(ScopeWhile, st, st.StmtsSpan, st.Stmts)
	case *ast.RepeatStmt:
		if st == nil {
			return
		}
		span := st.StmtsSpan
		if end := outerEnd(st.Condition); end > span.End {
			span.End = end
		}
		r.openScope(ScopeRepeat, st, span)
		r.stmtList(st.Stmts, false)
		r.expr(st.Condition) // sees the locals of the body
		r.closeScope()
	case *ast.IfStmt:
		for st != nil {
			r.expr(st.Condition)
			r.block(ScopeThen, st, st.ThenSpan, st.Then)
			if len(st.Else) == 1 {
				if ei, ok := st.Else[0].(*ast.IfStmt); ok && ei != nil { // an elseif
					st = ei
					continue
				}
			}
			if len(st.Else) > 0 {
				r.block(ScopeElse, st, st.ElseSpan, st.Else)
			}
			break
		}
	case *ast.NumberForStmt:
		if st == nil {
			return
		}
		v := r.declare(st.Name, KindForNum, st, 0, st.NameSpan)
		r.expr(st.Init)
		r.expr(st.Limit)
		r.expr(st.Step)
		r.openScope(ScopeFor, st, st.StmtsSpan)
		r.activate(v)
		r.block(ScopeForBody, st, st.StmtsSpan, st.Stmts)
		r.closeScope()
	case *ast.GenericForStmt:
		if st == nil {
			return
		}
		var buf [8]*Var
		vars := buf[:0]
		for i, name := range st.Names {
			vars = append(vars, r.declare(name, KindForIn, st, i, spanAt(st.NameSpans, i)))
		}
		r.exprs(st.Exprs)
		r.openScope(ScopeFor, st, st.StmtsSpan)
		for _, v := range vars {
			r.activate(v)
		}
		r.block(ScopeForBody, st, st.StmtsSpan, st.Stmts)
		r.closeScope()
	case *ast.FuncDefStmt:
		if st == nil || st.Name == nil {
			if st != nil {
				r.function(st.Func, false)
			}
			return
		}
		method := false
		switch {
		case st.Name.Func != nil:
			if id, ok := st.Name.Func.(*ast.IdentExpr); ok && id != nil {
				r.ref(id, Write)
			} else {
				r.expr(st.Name.Func)
			}
		case st.Name.Receiver != nil:
			r.expr(st.Name.Receiver)
			method = true
		}
		r.function(st.Func, method)
	case *ast.ReturnStmt:
		if st == nil {
			return
		}
		r.exprs(st.Exprs)
	case *ast.GotoStmt:
		if st == nil {
			return
		}
		r.goto_(st)
	}
}

func spanAt(spans []ast.Span, i int) ast.Span {
	if i < len(spans) {
		return spans[i]
	}
	return ast.Span{}
}

func (r *resolver) localAssign(st *ast.LocalAssignStmt) {
	var buf [8]*Var
	vars := buf[:0]
	fn := st.IsFunction && len(st.Names) == 1 && len(st.Exprs) == 1
	if fn {
		_, fn = st.Exprs[0].(*ast.FunctionExpr)
	}
	kind := KindLocal
	if fn {
		kind = KindLocalFunction
	}
	for i, name := range st.Names {
		vars = append(vars, r.declare(name, kind, st, i, spanAt(st.NameSpans, i)))
	}
	if fn { // "local function f": f is visible in its body
		r.activate(vars[0])
		r.expr(st.Exprs[0])
		return
	}
	r.exprs(st.Exprs)
	for _, v := range vars {
		r.activate(v)
	}
}

func (r *resolver) goto_(st *ast.GotoStmt) {
	if r.dialect != LuaJIT {
		r.problem(st, st.LabelSpan, "goto needs LuaJIT")
		return
	}
	r.info.Refs = append(r.info.Refs, Ref{Kind: Jump, Goto: st, Scope: r.cur})
	r.gola = append(r.gola, golaEntry{name: st.Label, valid: true, slot: r.nact(), ref: len(r.info.Refs) - 1})
	r.blocks[len(r.blocks)-1].hasGola = true
}

// label declares a label (parse_label); trailing says whether it is at the
// end of its block.
func (r *resolver) label(st *ast.LabelStmt, trailing bool) {
	if r.dialect != LuaJIT {
		r.problem(st, st.NameSpan, "labels need LuaJIT")
		return
	}
	bl := &r.blocks[len(r.blocks)-1]
	v := r.declare(st.Name, KindLabel, st, 0, st.NameSpan)
	v.Scope = r.cur
	for i := bl.vstart; i < len(r.gola); i++ {
		if g := &r.gola[i]; g.valid && g.isLabel && g.name == st.Name {
			r.problem(st, st.NameSpan, "duplicate label '%s'", st.Name)
			return
		}
	}
	slot := r.nact()
	if trailing { // a label at the end of a block is out of the scope of its locals
		slot = bl.nact
	}
	bl.hasGola = true
	idx := len(r.gola)
	r.gola = append(r.gola, golaEntry{name: st.Name, isLabel: true, valid: true, slot: slot, label: v})
	// the pending gotos of the block before it (gola_resolve)
	for i := bl.vstart; i < idx; i++ {
		g := &r.gola[i]
		if !g.valid || g.isLabel || g.name != st.Name {
			continue
		}
		if g.slot < slot {
			ref := &r.info.Refs[g.ref]
			local := "?"
			if k := r.fn.actBase + g.slot; k < len(r.act) {
				local = r.act[k].Name
			}
			r.problem(ref.Goto, ref.Span(), "<goto %s> jumps into the scope of local '%s'", g.name, local)
		}
		r.bind(g, v)
	}
}

/* expressions */

// outerEnd returns the end of an expression with its parentheses, 0 for a
// nil one (also a nil pointer).
func outerEnd(e ast.Expr) (end int) {
	if e == nil {
		return 0
	}
	defer func() {
		if recover() != nil {
			end = 0
		}
	}()
	return e.OuterSpan().End
}

func (r *resolver) exprs(list []ast.Expr) {
	for _, e := range list {
		r.expr(e)
	}
}

func (r *resolver) expr(e ast.Expr) {
	if r.depth >= maxDepth {
		if e != nil {
			r.tooDeep(e)
		}
		return
	}
	r.depth++
	r.expr1(e)
	r.depth--
}

func (r *resolver) expr1(e ast.Expr) {
	switch x := e.(type) {
	case *ast.IdentExpr:
		if x != nil {
			r.ref(x, Read)
		}
	case *ast.AttrGetExpr:
		if x != nil {
			r.expr(x.Object)
			r.expr(x.Key)
		}
	case *ast.FuncCallExpr:
		if x != nil {
			if x.Func != nil {
				r.expr(x.Func)
			} else {
				r.expr(x.Receiver)
			}
			r.exprs(x.Args)
		}
	case *ast.TableExpr:
		if x != nil {
			for _, f := range x.Fields {
				if f != nil {
					r.expr(f.Key)
					r.expr(f.Value)
				}
			}
		}
	case *ast.LogicalOpExpr:
		if x != nil {
			r.expr(x.Lhs)
			r.expr(x.Rhs)
		}
	case *ast.RelationalOpExpr:
		if x != nil {
			r.expr(x.Lhs)
			r.expr(x.Rhs)
		}
	case *ast.ArithmeticOpExpr:
		if x != nil {
			r.expr(x.Lhs)
			r.expr(x.Rhs)
		}
	case *ast.StringConcatOpExpr:
		if x != nil {
			r.expr(x.Lhs)
			r.expr(x.Rhs)
		}
	case *ast.UnaryMinusOpExpr:
		if x != nil {
			r.expr(x.Expr)
		}
	case *ast.UnaryNotOpExpr:
		if x != nil {
			r.expr(x.Expr)
		}
	case *ast.UnaryLenOpExpr:
		if x != nil {
			r.expr(x.Expr)
		}
	case *ast.FunctionExpr:
		r.function(x, false)
	}
}

// function resolves a function; method gives it the implicit "self".
func (r *resolver) function(fe *ast.FunctionExpr, method bool) {
	if fe == nil {
		return
	}
	if r.depth >= maxDepth {
		r.tooDeep(fe)
		return
	}
	r.depth++
	fs := r.openFunc(fe, fe.Span())
	if method {
		r.activate(r.declare("self", KindSelf, fe, -1, ast.Span{}))
	}
	pl := fe.ParList
	if pl != nil {
		for i, name := range pl.Names {
			r.activate(r.declare(name, KindParam, fe, i, spanAt(pl.NameSpans, i)))
		}
		if pl.HasVargs && r.dialect == Lua51 && !pl.NoArg && fs.parent != nil { // not the main function
			r.activate(r.declare("arg", KindArg, fe, -1, ast.Span{}))
		}
	}
	r.stmtList(fe.Stmts, true)
	r.closeFunc(fs)
	r.depth--
}

/* the result */

// finish builds what the lists of a variable and a scope are carved from.
func (r *resolver) finish() {
	in := r.info
	// the occurrences of each variable
	total := 0
	for _, v := range in.Vars {
		total += int(v.nr + v.nw)
	}
	backing := make([]*Ref, total)
	off := 0
	for _, v := range in.Vars {
		n, w := int(v.nr), int(v.nw)
		v.Reads = backing[off : off : off+n]
		v.Writes = backing[off+n : off+n : off+n+w]
		off += n + w
	}
	var globals map[string]*Global
	for i := range in.Refs {
		ref := &in.Refs[i]
		switch {
		case ref.Var != nil && ref.Kind == Write:
			ref.Var.Writes = append(ref.Var.Writes, ref)
		case ref.Var != nil:
			ref.Var.Reads = append(ref.Var.Reads, ref)
		case ref.Kind != Jump:
			if globals == nil {
				globals = map[string]*Global{}
			}
			name := ref.Ident.Value
			g := globals[name]
			if g == nil {
				g = &Global{Name: name}
				globals[name] = g
				in.Globals = append(in.Globals, g)
			}
			if ref.Kind == Write {
				g.Writes = append(g.Writes, ref)
			} else {
				g.Reads = append(g.Reads, ref)
			}
		}
	}
	// the variables and children of each scope
	for _, v := range in.Vars {
		v.Scope.nv++
		if v.Shadows != nil {
			in.Shadowing = append(in.Shadowing, v)
		}
	}
	nv, nc := 0, 0
	for _, s := range in.Scopes {
		nv += s.nv
		if s.Parent != nil {
			s.Parent.nc++
			nc++
		}
	}
	vb := make([]*Var, nv)
	cb := make([]*Scope, nc)
	nv, nc = 0, 0
	for _, s := range in.Scopes {
		s.Vars = vb[nv : nv : nv+s.nv]
		nv += s.nv
		s.Children = cb[nc : nc : nc+s.nc]
		nc += s.nc
	}
	for _, v := range in.Vars {
		v.Scope.Vars = append(v.Scope.Vars, v)
	}
	for _, s := range in.Scopes {
		if s.Parent != nil {
			s.Parent.Children = append(s.Parent.Children, s)
		}
	}
}
