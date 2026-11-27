package lua

// go-moonscript patch: fuzz test of CompileAST / LState.LoadAST: parsed
// trees, mutated (nodes removed, replaced by other nodes of the tree or by
// nodes that break the parser's guarantees, fields changed, lines dropped)
// load without a panic, as a *CompileError or as a function that runs, or
// fails with a Lua error, but no Go panic in the VM. TestCompileASTMutations
// runs the mutations of a fixed pseudo-random sequence; `go test -fuzz
// FuzzCompileAST` searches further.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
)

// fuzzSeeds are the sources the fuzz test mutates the trees of: all the
// kinds of nodes, scopes, loops, closures, varargs, methods, gotos.
var fuzzSeeds = []string{
	`local a, b = 1, 2; a, b = b, a; return a - b`,
	`local t = {1, 2, x = 3, [4] = 5, f(), ...}; return #t, t.x, t[4]`,
	`local s = 0; for i = 1, 10, 2 do s = s + i end; return s`,
	`local s = 0; for k, v in pairs({a = 1, b = 2}) do s = s + v end; return s`,
	`local i = 0; while i < 5 do i = i + 1; if i == 3 then break end end; return i`,
	`local i = 0; repeat local j = i; i = i + 1 until j >= 3; return i`,
	`local function f(n) if n < 2 then return n end return f(n - 1) + f(n - 2) end; return f(10)`,
	`local o = {v = 1}; function o:get() return self.v end; function o.set(x) o.v = x end; o.set(2); return o:get()`,
	`local function count(...) local n = select('#', ...); return n, ... end; return count(1, nil, 3)`,
	`local function g(...) local t = {...}; return arg, #t end; return g(1, 2)`,
	`local x = 1; local function a() local function b() return x end; x = x + 1; return b() end; return a()`,
	`return not nil, not 1, -2 ^ 2, 2 ^ -1, 7 % 3, -7 % 3, 1 / 0, "a" .. "b" .. 1 .. 2`,
	`local a, b = 1, nil; return a and b, a or b, b and a, b or a, a and b or 3, (a > 0) and "p" or "n"`,
	`local t = setmetatable({}, {__index = function(t, k) return k .. "!" end}); return t.x, t[1]`,
	`local co = coroutine.wrap(function(a) local b = coroutine.yield(a + 1); return b * 2 end); return co(1), co(5)`,
	`local ok, err = pcall(error, {code = 1}); return ok, type(err), err.code`,
	`local s = ("x"):rep(3):upper(); return s:len(), s:sub(2), string.format("%5.2f", 3.14159)`,
	`local t = {}; for i = 1, 5 do t[#t + 1] = function() return i end end; return t[1](), t[5]()`,
	`local f = loadstring("return 1 + ..."); return f(2)`,
	`if a then b = 1 elseif c then b = 2 elseif d then b = 3 else b = 4 end; return b`,
	`do local x = 1; do local y = x + 1; z = y end end; return z`,
	`local t = {}; t.a = {}; t.a.b = {}; t.a.b.c = 1; t["a"].b["c"] = t.a.b.c + 1; return t.a.b.c`,
	`local t = {}; t[1], t[2] = 1, 2; local a = t; a.x, a = 1, 2; return t.x, a`,
	`local i = 1; ::top:: i = i + 1; if i < 5 then goto top end; return i`,
	`for i = 1, 3 do for j = 1, 3 do if j == 2 then goto continue end; x = (x or 0) + j; ::continue:: end end; return x`,
	`do goto done; local x = 1; ::done:: end; return 0x10, 1e2, 0x1p4`,
	`local t = {n = 0}; function t.inc(self, d) self.n = self.n + (d or 1); return self end; return t:inc():inc(2).n`,
	`return (f or print)(("%d"):format(#{1, 2}))`,
}

// fuzzNames are the names the mutations put in name, label and method
// fields.
var fuzzNames = []string{"a", "b", "x", "i", "t", "f", "self", "arg", "print", "top", "continue", "done", "", "_ENV"}

// fuzzOperators are the operators the mutations put in operator fields,
// some of them not Lua's.
var fuzzOperators = []string{"+", "-", "*", "/", "%", "^", "==", "~=", "<", "<=", ">", ">=", "and", "or", "..", "//", ""}

// fuzzLines are the lines the mutations give nodes.
var fuzzLines = []int{0, 0, 0, 1, 2, 7, 100, 1 << 30, -1}

var (
	exprType  = reflect.TypeFor[ast.Expr]()
	stmtType  = reflect.TypeFor[ast.Stmt]()
	posType   = reflect.TypeFor[ast.PositionHolder]()
	stmtsType = reflect.TypeFor[[]ast.Stmt]()
	exprsType = reflect.TypeFor[[]ast.Expr]()
)

// deepCopy copies a tree (a tree of pointers, slices and interfaces).
func deepCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		n := reflect.New(v.Type().Elem())
		n.Elem().Set(v.Elem())
		if n.Elem().Kind() == reflect.Struct {
			s := n.Elem()
			for i := range s.NumField() {
				if f := s.Field(i); f.CanSet() {
					f.Set(deepCopy(f))
				}
			}
		}
		return n
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		r := reflect.New(v.Type()).Elem()
		r.Set(deepCopy(v.Elem()))
		return r
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		n := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			n.Index(i).Set(deepCopy(v.Index(i)))
		}
		return n
	}
	return v
}

// slots are the places of a tree a mutation changes: settable values.
type slots struct {
	exprs   []reflect.Value // fields and elements of type ast.Expr
	stmts   []reflect.Value // slices of statements
	elists  []reflect.Value // slices of expressions
	ptrs    []reflect.Value // pointer fields: *ast.Field, *ast.ParList, *ast.FuncName, *ast.FunctionExpr
	strs    []reflect.Value // string fields
	names   []reflect.Value // []string fields
	bools   []reflect.Value
	nodes   []ast.PositionHolder
	donorsE []reflect.Value // expressions to copy
	donorsS []reflect.Value // statements to copy
}

func (s *slots) collect(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		if ph, ok := v.Interface().(ast.PositionHolder); ok {
			s.nodes = append(s.nodes, ph)
		}
		s.collect(v.Elem())
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		switch {
		case v.Type() == exprType:
			s.donorsE = append(s.donorsE, v.Elem())
		case v.Type() == stmtType:
			s.donorsS = append(s.donorsS, v.Elem())
		}
		s.collect(v.Elem())
	case reflect.Struct:
		for i := range v.NumField() {
			f := v.Field(i)
			if !f.CanSet() {
				continue
			}
			switch {
			case f.Type() == exprType:
				s.exprs = append(s.exprs, f)
			case f.Kind() == reflect.Pointer:
				s.ptrs = append(s.ptrs, f)
			case f.Kind() == reflect.String:
				s.strs = append(s.strs, f)
			case f.Kind() == reflect.Bool:
				s.bools = append(s.bools, f)
			case f.Type() == reflect.TypeFor[[]string]():
				s.names = append(s.names, f)
			}
			s.collect(f)
		}
	case reflect.Slice:
		switch v.Type() {
		case stmtsType:
			s.stmts = append(s.stmts, v)
		case exprsType:
			s.elists = append(s.elists, v)
		}
		for i := range v.Len() {
			e := v.Index(i)
			if e.Type() == exprType {
				s.exprs = append(s.exprs, e)
			}
			s.collect(e)
		}
	}
}

// mutator mutates a tree as its bytes say.
type mutator struct {
	ops []byte
}

func (m *mutator) next() int {
	if len(m.ops) == 0 {
		return 0
	}
	b := m.ops[0]
	m.ops = m.ops[1:]
	return int(b)
}

func pick[T any](m *mutator, list []T) (T, bool) {
	var zero T
	if len(list) == 0 {
		return zero, false
	}
	return list[m.next()%len(list)], true
}

// expr is an expression to put into the tree: a copy of one of it, or one
// the parser does not build.
func (m *mutator) expr(s *slots) ast.Expr {
	switch m.next() % 24 {
	case 0:
		return nil
	case 1:
		return &ast.Comma3Expr{}
	case 2:
		return &ast.CdataNumberExpr{Value: []string{"1LL", "2ULL", "3i", ""}[m.next()%4]}
	case 3:
		return &unknownExpr{}
	case 4:
		return &ast.NumberExpr{Value: []string{"1x", "", "0x", "nan", "inf", "1e400", " 2 "}[m.next()%7]}
	case 5:
		return &ast.FunctionExpr{ParList: []*ast.ParList{nil, {}, {HasVargs: true}, {Names: []string{"a", "a"}}}[m.next()%4]}
	case 6:
		return nested(m.next()*4, &ast.Comma3Expr{})
	case 7:
		return &ast.FuncCallExpr{Receiver: ident("t"), Method: "m", Func: []ast.Expr{nil, ident("f")}[m.next()%2]}
	case 8:
		return call(ident("f"), make([]ast.Expr, m.next()%2)...)
	case 9:
		args := make([]ast.Expr, 200+m.next())
		for i := range args {
			args[i] = &ast.Comma3Expr{AdjustRet: i%2 == 0}
		}
		return call(ident("f"), args...)
	case 10:
		return &ast.TableExpr{Fields: []*ast.Field{nil, {Key: str("k")}, {Value: &ast.Comma3Expr{}}}[m.next()%3:]}
	case 11:
		return ident(fuzzNames[m.next()%len(fuzzNames)])
	}
	if d, ok := pick(m, s.donorsE); ok {
		return deepCopy(d).Interface().(ast.Expr)
	}
	return &ast.NilExpr{}
}

// stmt is a statement to put into the tree.
func (m *mutator) stmt(s *slots) ast.Stmt {
	switch m.next() % 20 {
	case 0:
		return nil
	case 1:
		return &ast.BreakStmt{}
	case 2:
		return &ast.GotoStmt{Label: fuzzNames[m.next()%len(fuzzNames)]}
	case 3:
		return &ast.LabelStmt{Name: fuzzNames[m.next()%len(fuzzNames)]}
	case 4:
		return ret(m.expr(s))
	case 5:
		return &unknownStmt{}
	case 6:
		names := make([]string, m.next()+m.next())
		for i := range names {
			names[i] = fmt.Sprintf("l%d", i)
		}
		return local(names)
	case 7:
		return &ast.DoBlockStmt{Stmts: manyUpvalues(40 + m.next()%30)}
	case 8:
		return &ast.FuncCallStmt{Expr: m.expr(s)}
	case 9:
		return &ast.AssignStmt{Lhs: []ast.Expr{m.expr(s)}, Rhs: []ast.Expr{m.expr(s)}}
	}
	if d, ok := pick(m, s.donorsS); ok {
		return deepCopy(d).Interface().(ast.Stmt)
	}
	return &ast.DoBlockStmt{}
}

// mutate applies the mutations of m's bytes to the tree.
func (m *mutator) mutate(chunk []ast.Stmt) []ast.Stmt {
	root := reflect.ValueOf(&chunk).Elem()
	for n := 0; len(m.ops) > 0 && n < 12; n++ {
		var s slots
		s.collect(root)
		s.stmts = append(s.stmts, root)
		switch m.next() % 11 {
		case 0: // an expression replaced
			if v, ok := pick(m, s.exprs); ok {
				e := m.expr(&s)
				if e == nil {
					v.SetZero()
				} else {
					v.Set(reflect.ValueOf(e))
				}
			}
		case 1, 2: // a statement inserted or replaced
			if v, ok := pick(m, s.stmts); ok {
				st := m.stmt(&s)
				i := m.next() % (v.Len() + 1)
				if i < v.Len() && m.next()%2 == 0 {
					v.Index(i).Set(reflect.ValueOf(&st).Elem())
				} else {
					old := v.Interface().([]ast.Stmt)
					v.Set(reflect.ValueOf(append(append(append([]ast.Stmt{}, old[:i]...), st), old[i:]...)))
				}
			}
		case 3: // a statement removed
			if v, ok := pick(m, s.stmts); ok && v.Len() > 0 {
				i := m.next() % v.Len()
				v.Set(reflect.AppendSlice(v.Slice(0, i), v.Slice(i+1, v.Len())))
			}
		case 4: // an expression list changed
			if v, ok := pick(m, s.elists); ok {
				if v.Len() > 0 && m.next()%2 == 0 {
					v.Set(v.Slice(0, v.Len()-1))
				} else {
					e := m.expr(&s)
					v.Set(reflect.Append(v, reflect.ValueOf(&e).Elem()))
				}
			}
		case 5: // a pointer field made nil
			if v, ok := pick(m, s.ptrs); ok {
				v.SetZero()
			}
		case 6: // a string field changed
			if v, ok := pick(m, s.strs); ok {
				if m.next()%2 == 0 {
					v.SetString(fuzzOperators[m.next()%len(fuzzOperators)])
				} else {
					v.SetString(fuzzNames[m.next()%len(fuzzNames)])
				}
			}
		case 7: // a bool field flipped
			if v, ok := pick(m, s.bools); ok {
				v.SetBool(!v.Bool())
			}
		case 8: // names changed
			if v, ok := pick(m, s.names); ok {
				names := append([]string(nil), v.Interface().([]string)...)
				if len(names) > 0 && m.next()%2 == 0 {
					names = names[:len(names)-1]
				} else {
					names = append(names, fuzzNames[m.next()%len(fuzzNames)])
				}
				v.Set(reflect.ValueOf(names))
			}
		case 9, 10: // lines changed
			if n, ok := pick(m, s.nodes); ok {
				if m.next()%2 == 0 {
					n.SetLine(fuzzLines[m.next()%len(fuzzLines)])
				} else {
					n.SetLastLine(fuzzLines[m.next()%len(fuzzLines)])
				}
			}
		}
	}
	return chunk
}

// fuzzState is a state for running mutated code: the base, table, string,
// math and coroutine libraries without the functions that reach out of
// it or take long, limits of memory, stack and time.
func fuzzState(t *testing.T, dialect Dialect) (*LState, context.CancelFunc) {
	t.Helper()
	L := NewState(Options{Dialect: dialect, SkipOpenLibs: true, CallStackSize: 200, RegistrySize: 1024, RegistryMaxSize: 1 << 18})
	for _, lib := range []luaLib{{LoadLibName, OpenPackage}, {BaseLibName, OpenBase}, {TabLibName, OpenTable}, {StringLibName, OpenString},
		{MathLibName, OpenMath}, {CoroutineLibName, OpenCoroutine}} {
		L.Push(L.NewFunction(lib.libFunc))
		L.Push(LString(lib.libName))
		L.Call(1, 0)
	}
	for _, name := range []string{"dofile", "loadfile", "require", "collectgarbage", "gcinfo", "module", "package"} {
		L.SetGlobal(name, LNil)
	}
	L.SetGlobal("print", L.NewFunction(func(*LState) int { return 0 }))
	L.SetMemoryLimit(64 << 20)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	L.SetContext(ctx)
	return L, func() { cancel(); L.Close() }
}

// The outcomes of a mutant.
const (
	mutantUnparsed = iota // its seed is of the other dialect
	mutantRejected        // a *CompileError
	mutantRan             // it ran
	mutantFailed          // it failed with a Lua error
	mutantOutcomes
)

// checkMutant loads a tree with LoadAST and runs it: it must fail with a
// *CompileError, or run, or fail with a Lua error.
func checkMutant(t *testing.T, chunk []ast.Stmt, dialect Dialect) int {
	t.Helper()
	L, done := fuzzState(t, dialect)
	defer done()
	fn, err := L.LoadAST(chunk, "=fuzz")
	if err != nil {
		var ae *ApiError
		if !errors.As(err, &ae) {
			t.Fatalf("LoadAST: %v (%T)", err, err)
		}
		if ce, ok := ae.Cause.(*CompileError); !ok || strings.Contains(ce.Message, "internal compiler error") {
			t.Fatalf("LoadAST: %v (%T)\n%s", err, ae.Cause, treeString(chunk))
		}
		return mutantRejected
	}
	L.Push(fn)
	if err := L.PCall(0, MultRet, nil); err != nil {
		var ae *ApiError
		if !errors.As(err, &ae) || ae.Type == ApiErrorPanic {
			t.Fatalf("run: %v\n%s", err, treeString(chunk))
		}
		return mutantFailed
	}
	return mutantRan
}

// fuzzMutant parses the seed sel picks (and the dialect, its low bit),
// mutates it with ops and checks it.
func fuzzMutant(t *testing.T, sel uint8, ops []byte) int {
	dialect := DialectLua51
	if sel&1 != 0 {
		dialect = DialectLuaJIT
	}
	src := fuzzSeeds[int(sel>>1)%len(fuzzSeeds)]
	chunk, err := parse.ParseBytes([]byte(src), "fuzz", parseDialect(dialect))
	if err != nil {
		return mutantUnparsed
	}
	chunk = (&mutator{ops: ops}).mutate(chunk)
	return checkMutant(t, chunk, dialect)
}

func FuzzCompileAST(f *testing.F) {
	for i := range fuzzSeeds {
		f.Add(uint8(2*i), []byte{})
		f.Add(uint8(2*i+1), []byte{byte(i), 1, 2, 3, byte(i * 7)})
	}
	f.Fuzz(func(t *testing.T, sel uint8, ops []byte) { fuzzMutant(t, sel, ops) })
}

// TestCompileASTMutations checks the mutants of a fixed pseudo-random
// sequence of seeds and mutations.
func TestCompileASTMutations(t *testing.T) {
	n := 3000
	if testing.Short() {
		n = 300
	}
	r := rand.New(rand.NewPCG(1, 2))
	var outcomes [mutantOutcomes]int
	for range n {
		ops := make([]byte, r.IntN(24))
		for i := range ops {
			ops[i] = byte(r.IntN(256))
		}
		outcomes[fuzzMutant(t, uint8(r.IntN(256)), ops)]++
	}
	t.Logf("%d mutants: %d rejected, %d ran, %d failed at run time (%d seeds of the other dialect)",
		n, outcomes[mutantRejected], outcomes[mutantRan], outcomes[mutantFailed], outcomes[mutantUnparsed])
}
