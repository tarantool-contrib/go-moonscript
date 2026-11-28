// Package globals finds the global variables and modules Lua code uses.
//
// The analysis is static. Names are resolved by package scope with the
// rules of the compiler, so a global here is exactly a GGET or GSET in the
// bytecode of LuaJIT. Field accesses are followed through constant keys:
// "box.space.users:select()" uses the path box.space.users.select.
package globals

import (
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
	"github.com/tarantool-contrib/go-moonscript/luascope"
)

// Segment is an element of a path: a global name or a field name. A
// field indexed with a non-constant key is Dynamic and has no name.
type Segment struct {
	Name    string
	Dynamic bool
}

// Path is a global variable followed by the fields accessed through it.
type Path []Segment

// String formats the path like Lua code: box.space["my space"][?].
func (p Path) String() string {
	var b strings.Builder

	for i, s := range p {
		switch {
		case s.Dynamic:
			b.WriteString("[?]")
		case i == 0:
			b.WriteString(s.Name)
		case isIdent(s.Name):
			b.WriteString("." + s.Name)
		default:
			b.WriteString("[" + strconv.Quote(s.Name) + "]")
		}
	}

	return b.String()
}

func isIdent(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}

	for i := range len(s) {
		c := s[i] | 0x20
		if (c < 'a' || c > 'z') && (s[i] < '0' || s[i] > '9') && s[i] != '_' {
			return false
		}
	}

	return true
}

// Access is a read or a write of a global variable.
type Access struct {
	Path  Path
	Write bool
	// Span is the span of the global name the path starts with, Pos the
	// position of its start. A tree without spans has the zero Span and
	// the zero Pos.
	Span ast.Span
	Pos  ast.Pos
}

// Require is a call of the global require: "require(name)", or a protected
// call of it with the global pcall or xpcall: "pcall(require, name)" or
// "xpcall(require, handler, name)". The require may be named through the
// constant fields of the global _G, the table of globals: "_G.require(name)"
// and "pcall(_G['require'], name)" are calls of it too.
type Require struct {
	// Module is the module name if it is a constant.
	Module string
	// Dynamic is set when the module name is not a constant.
	Dynamic bool
	// Span is the span of the name that starts the require, of _G in
	// "_G.require(name)", and Pos the position of its start.
	Span ast.Span
	Pos  ast.Pos
}

// Usage lists the global variables and modules a chunk uses.
type Usage struct {
	Accesses []Access
	Requires []Require
	// RequireValues are uses of the global require other than calls
	// recorded in Requires, such as "local r = require", "f(require, name)"
	// or "local r = _G.require": the reads of require, or of a field of it,
	// that are also in Accesses.
	RequireValues []Access
}

// RequireName is the name of the global require function.
const RequireName = "require"

// globalTable is the name of the global that holds the table of globals.
const globalTable = "_G"

// Analyze parses and resolves a chunk with luascope.Parse and returns the
// global variables and modules it uses. A syntax error is a *parse.Error.
func Analyze(src []byte, name string, opts luascope.Options) (*Usage, error) {
	chunk, info, err := luascope.Parse(src, name, opts)
	if err != nil {
		return nil, err
	}

	return AnalyzeChunk(chunk, info), nil
}

// AnalyzeChunk collects the global variables and modules used by a chunk
// that is already parsed and resolved: info must be the resolution of
// chunk, as luascope.Parse or scope.Resolve return it. Every use of a
// global name produces exactly one Access. The positions are those of
// chunk.Lines, none if the chunk has no Lines.
func AnalyzeChunk(chunk *ast.Chunk, info *scope.Info) *Usage {
	a := &analyzer{lines: chunk.Lines, globals: globalNames(info)}
	a.stmts(chunk.Stmts)

	return &a.usage
}

// globalNames returns the occurrences of global names of a resolution.
func globalNames(info *scope.Info) map[*ast.IdentExpr]struct{} {
	n := 0
	for _, g := range info.Globals {
		n += len(g.Reads) + len(g.Writes)
	}

	names := make(map[*ast.IdentExpr]struct{}, n)

	for _, g := range info.Globals {
		for _, r := range g.Reads {
			names[r.Ident] = struct{}{}
		}

		for _, r := range g.Writes {
			names[r.Ident] = struct{}{}
		}
	}

	return names
}

type analyzer struct {
	usage   Usage
	lines   *ast.Lines
	globals map[*ast.IdentExpr]struct{}
}

// global returns e as a name and reports whether it is the name of a
// global; parentheses around it do not matter.
func (a *analyzer) global(e ast.Expr) (*ast.IdentExpr, bool) {
	id, ok := e.(*ast.IdentExpr)
	if !ok {
		return nil, false
	}

	return id, a.isGlobal(id)
}

// isGlobal reports whether a name is a global.
func (a *analyzer) isGlobal(id *ast.IdentExpr) bool {
	_, ok := a.globals[id]
	return ok
}

func (a *analyzer) stmts(list []ast.Stmt) {
	for _, s := range list {
		a.stmt(s)
	}
}

func (a *analyzer) exprs(list []ast.Expr) {
	for _, e := range list {
		a.expr(e)
	}
}

// stmt walks a statement in source order. Nil nodes, also nil pointers,
// are skipped, as package scope skips them.
func (a *analyzer) stmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.AssignStmt:
		if n != nil {
			for _, t := range n.Lhs {
				a.target(t, "", false)
			}

			a.exprs(n.Rhs)
		}
	case *ast.LocalAssignStmt:
		if n != nil {
			a.exprs(n.Exprs)
		}
	case *ast.FuncCallStmt:
		if n != nil {
			a.expr(n.Expr)
		}
	case *ast.FuncDefStmt:
		if n != nil {
			a.funcDef(n)
		}
	case *ast.ReturnStmt:
		if n != nil {
			a.exprs(n.Exprs)
		}
	default:
		a.block(s)
	}
}

// block walks a statement that holds a block.
func (a *analyzer) block(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.DoBlockStmt:
		if n != nil {
			a.stmts(n.Stmts)
		}
	case *ast.WhileStmt:
		if n != nil {
			a.expr(n.Condition)
			a.stmts(n.Stmts)
		}
	case *ast.RepeatStmt:
		if n != nil {
			a.stmts(n.Stmts)
			a.expr(n.Condition)
		}
	case *ast.IfStmt:
		if n != nil {
			a.expr(n.Condition)
			a.stmts(n.Then)
			a.stmts(n.Else)
		}
	case *ast.NumberForStmt:
		if n != nil {
			a.expr(n.Init)
			a.expr(n.Limit)
			a.expr(n.Step)
			a.stmts(n.Stmts)
		}
	case *ast.GenericForStmt:
		if n != nil {
			a.exprs(n.Exprs)
			a.stmts(n.Stmts)
		}
	}
}

// funcDef records the name of a function statement, a write, and walks
// its function.
func (a *analyzer) funcDef(n *ast.FuncDefStmt) {
	if name := n.Name; name != nil {
		if name.Receiver != nil {
			a.target(name.Receiver, name.Method, true)
		} else {
			a.target(name.Func, "", false)
		}
	}

	if n.Func != nil {
		a.stmts(n.Func.Stmts)
	}
}

// expr walks an expression in source order.
func (a *analyzer) expr(e ast.Expr) {
	switch n := e.(type) {
	case *ast.IdentExpr:
		if n != nil && a.isGlobal(n) {
			a.access(Path{{Name: n.Value}}, false, n)
		}
	case *ast.AttrGetExpr:
		if n != nil {
			a.chain(n, "", false, false)
		}
	case *ast.FuncCallExpr:
		if n != nil {
			a.call(n)
		}
	case *ast.FunctionExpr:
		if n != nil {
			a.stmts(n.Stmts)
		}
	case *ast.TableExpr:
		if n != nil {
			for _, f := range n.Fields {
				if f != nil {
					a.expr(f.Key)
					a.expr(f.Value)
				}
			}
		}
	default:
		a.operator(e)
	}
}

// operator walks the operands of an operator.
func (a *analyzer) operator(e ast.Expr) {
	switch n := e.(type) {
	case *ast.LogicalOpExpr:
		if n != nil {
			a.expr(n.Lhs)
			a.expr(n.Rhs)
		}
	case *ast.RelationalOpExpr:
		if n != nil {
			a.expr(n.Lhs)
			a.expr(n.Rhs)
		}
	case *ast.StringConcatOpExpr:
		if n != nil {
			a.expr(n.Lhs)
			a.expr(n.Rhs)
		}
	case *ast.ArithmeticOpExpr:
		if n != nil {
			a.expr(n.Lhs)
			a.expr(n.Rhs)
		}
	case *ast.UnaryMinusOpExpr:
		if n != nil {
			a.expr(n.Expr)
		}
	case *ast.UnaryNotOpExpr:
		if n != nil {
			a.expr(n.Expr)
		}
	case *ast.UnaryLenOpExpr:
		if n != nil {
			a.expr(n.Expr)
		}
	}
}

// call walks a call: a method call adds its method to the path of the
// receiver, a call of require is a Require.
func (a *analyzer) call(c *ast.FuncCallExpr) {
	switch {
	case c.Receiver != nil:
		a.chain(c.Receiver, c.Method, true, false)
	case a.requireCall(c):
		return // The arguments are walked.
	default:
		a.expr(c.Func)
	}

	a.exprs(c.Args)
}

// pos returns the span and the position of a name.
func (a *analyzer) pos(id *ast.IdentExpr) (ast.Span, ast.Pos) {
	span := id.Span()
	if a.lines == nil || span == (ast.Span{}) {
		return span, ast.Pos{}
	}

	return span, a.lines.Pos(span.Start)
}

func (a *analyzer) access(path Path, write bool, id *ast.IdentExpr) {
	span, pos := a.pos(id)
	access := Access{Path: path, Write: write, Span: span, Pos: pos}
	a.usage.Accesses = append(a.usage.Accesses, access)

	if !write && throughRequire(path) {
		a.usage.RequireValues = append(a.usage.RequireValues, access)
	}
}

// throughRequire reports whether a path reads the global require or a field
// of it, directly or through the constant fields of _G: require, require.x,
// _G.require and _G._G.require do, _G and _G.x.require do not.
func throughRequire(path Path) bool {
	i := 0
	for i < len(path)-1 && !path[i].Dynamic && path[i].Name == globalTable {
		i++
	}

	return !path[i].Dynamic && path[i].Name == RequireName
}

// chain records the access through a chain of fields rooted at a global
// name. A method call adds its method to the path (hasMethod). A chain with
// any other root is not an access; its root is walked instead. The
// non-constant keys are walked in both cases. A chain is analyzed once,
// from its outermost field, so the analysis is linear in its length.
func (a *analyzer) chain(e ast.Expr, method string, hasMethod, write bool) {
	root, fields, keys := splitChain(e)

	if id, ok := a.global(root); ok {
		path := append(Path{{Name: id.Value}}, fields...)
		if hasMethod {
			path = append(path, Segment{Name: method})
		}

		a.access(path, write, id)
	} else {
		a.expr(root)
	}

	a.exprs(keys)
}

// splitChain splits a chain of fields into its root, the fields after the
// root and the non-constant keys, both in source order. Parentheses do not
// break a chain, and, like LuaJIT, a string constant in parentheses is a
// constant key: box[("schema")] is box.schema.
func splitChain(e ast.Expr) (ast.Expr, Path, []ast.Expr) {
	var (
		fields Path
		keys   []ast.Expr
	)

	for {
		x, ok := e.(*ast.AttrGetExpr)
		if !ok || x == nil {
			slices.Reverse(fields)
			slices.Reverse(keys)

			return e, fields, keys
		}

		if s, ok := x.Key.(*ast.StringExpr); ok && s != nil {
			fields = append(fields, Segment{Name: s.Value})
		} else {
			fields = append(fields, Segment{Dynamic: true})
			keys = append(keys, x.Key)
		}

		e = x.Object
	}
}

// target records an assignment target, or the name of a function
// statement: with its method for "function a.b:m()".
func (a *analyzer) target(e ast.Expr, method string, hasMethod bool) {
	if id, ok := e.(*ast.IdentExpr); ok && !hasMethod {
		if a.isGlobal(id) {
			a.access(Path{{Name: id.Value}}, true, id)
		}

		return
	}

	a.chain(e, method, hasMethod, true)
}

// protectedCalls are the global functions that call their first argument,
// with the index of the first argument they pass to it.
var protectedCalls = map[string]int{"pcall": 1, "xpcall": 2}

// requireCall records a call of the global require, directly or through
// pcall or xpcall, and reports whether the call was one.
func (a *analyzer) requireCall(c *ast.FuncCallExpr) bool {
	if root, path, ok := a.requireFunc(c.Func); ok {
		a.require(root, path, c.Args, 0)
		return true
	}

	callee, ok := a.global(c.Func)
	if !ok || len(c.Args) == 0 {
		return false
	}

	arg, ok := protectedCalls[callee.Value]
	if !ok {
		return false
	}

	root, path, ok := a.requireFunc(c.Args[0])
	if !ok {
		return false
	}

	a.access(Path{{Name: callee.Value}}, false, callee)
	a.require(root, path, c.Args[1:], arg-1)

	return true
}

// require records a require of the function at path, which starts with the
// global name root, with the module name in args[i].
func (a *analyzer) require(root *ast.IdentExpr, path Path, args []ast.Expr, i int) {
	span, pos := a.pos(root)

	r := Require{Span: span, Pos: pos, Dynamic: true}
	if i < len(args) {
		var ok bool

		r.Module, ok = constString(args[i])
		r.Dynamic = !ok
	}

	a.usage.Requires = append(a.usage.Requires, r)
	a.usage.Accesses = append(a.usage.Accesses, Access{Path: path, Span: span, Pos: pos})
	a.exprs(args)
}

// requireFunc reports whether e is the global require, named directly or
// through the constant fields of _G: require, (require), _G.require,
// _G["require"] and _G._G.require are. It returns the global name e starts
// with and the path of e. It does not allocate unless e is the require.
func (a *analyzer) requireFunc(e ast.Expr) (*ast.IdentExpr, Path, bool) {
	want, fields := RequireName, 0

	for {
		switch x := e.(type) {
		case *ast.IdentExpr:
			if x == nil || x.Value != want || !a.isGlobal(x) {
				return nil, nil, false
			}

			path := make(Path, fields+1)
			for i := range fields {
				path[i] = Segment{Name: globalTable}
			}

			path[fields] = Segment{Name: RequireName}

			return x, path, true
		case *ast.AttrGetExpr:
			if x == nil {
				return nil, nil, false
			}

			key, ok := x.Key.(*ast.StringExpr)
			if !ok || key == nil || key.Value != want {
				return nil, nil, false
			}

			want = globalTable
			fields++
			e = x.Object
		default:
			return nil, nil, false
		}
	}
}

// constString evaluates string literals and their concatenations.
func constString(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.StringExpr:
		if x == nil {
			return "", false
		}

		return x.Value, true
	case *ast.StringConcatOpExpr:
		if x == nil {
			return "", false
		}

		l, okL := constString(x.Lhs)
		r, okR := constString(x.Rhs)

		return l + r, okL && okR
	default:
		return "", false
	}
}
