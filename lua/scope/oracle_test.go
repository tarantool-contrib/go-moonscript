package scope_test

// The compiler is the oracle: it resolves names as the parsers do, and
// leaves what it resolved in the function prototypes it builds: the upvalues
// by name and in order (DbgUpvalues), the locals (DbgLocals), and the
// globals and upvalues each instruction uses (GETGLOBAL, SETGLOBAL,
// GETUPVAL, SETUPVAL). For every function of every source, the resolver's
// answer must be the compiler's:
//
//   - the upvalues of the function are the names the compiler lists, in its
//     order, and each is a variable captured by the function;
//   - the names of the globals read and written directly in the function
//     are the constants of its GETGLOBAL and SETGLOBAL, as many times;
//   - the upvalues read and written are the operands of its GETUPVAL and
//     SETUPVAL, as many times;
//   - the variables declared in the function are its debug locals (the
//     hidden control variables of for loops left out).

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	lua "github.com/tarantool-contrib/go-moonscript/lua"
	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

func luaDialect(d scope.Dialect) lua.Dialect {
	if d == scope.LuaJIT {
		return lua.DialectLuaJIT
	}
	return lua.DialectLua51
}

// oracleStats counts what an oracle run compared.
type oracleStats struct {
	sources, parseErrors, compileErrors int
	functions, upvalues, globalRefs     int
	upvalueRefs, locals                 int
}

func (s *oracleStats) add(o oracleStats) {
	s.sources += o.sources
	s.parseErrors += o.parseErrors
	s.compileErrors += o.compileErrors
	s.functions += o.functions
	s.upvalues += o.upvalues
	s.globalRefs += o.globalRefs
	s.upvalueRefs += o.upvalueRefs
	s.locals += o.locals
}

// compilerOracle parses, resolves and compiles src, and returns the
// differences between the resolver and the compiler, with counts of what it
// compared.
func compilerOracle(src []byte, d scope.Dialect) (stats oracleStats, diffs []string) {
	stats.sources = 1
	chunk, err := parse.ParseChunk(src, "oracle", parse.Options{Dialect: parseDialect(d)})
	if err != nil {
		stats.parseErrors = 1
		return stats, nil
	}
	proto, err := lua.CompileAST(chunk.Stmts, "=oracle", luaDialect(d))
	if err != nil {
		stats.compileErrors = 1
		return stats, nil
	}
	info, err := scope.Resolve(chunk, d)
	if err != nil {
		return stats, []string{"resolve: " + err.Error()}
	}
	var funcs []*scope.Scope
	for _, s := range info.Scopes {
		if s.IsFunction() {
			funcs = append(funcs, s)
		}
	}
	var protos []*lua.FunctionProto
	var collect func(*lua.FunctionProto)
	collect = func(p *lua.FunctionProto) {
		protos = append(protos, p)
		for _, c := range p.FunctionPrototypes {
			collect(c)
		}
	}
	collect(proto)
	if len(funcs) != len(protos) {
		return stats, []string{fmt.Sprintf("%d function scopes, %d prototypes", len(funcs), len(protos))}
	}
	// per function: the references made directly in it
	globalReads := map[*scope.Scope]map[string]int{}
	globalWrites := map[*scope.Scope]map[string]int{}
	upReads := map[*scope.Scope]map[string]int{}
	upWrites := map[*scope.Scope]map[string]int{}
	bump := func(m map[*scope.Scope]map[string]int, s *scope.Scope, name string) {
		if m[s] == nil {
			m[s] = map[string]int{}
		}
		m[s][name]++
	}
	for i := range info.Refs {
		r := &info.Refs[i]
		f := r.Scope.Func
		switch {
		case r.Kind == scope.Jump:
		case r.Var == nil && r.Kind == scope.Read:
			bump(globalReads, f, r.Name())
		case r.Var == nil:
			bump(globalWrites, f, r.Name())
		case r.IsUpvalue() && r.Kind == scope.Read:
			bump(upReads, f, r.Name())
		case r.IsUpvalue():
			bump(upWrites, f, r.Name())
		}
	}
	for i, s := range funcs {
		p := protos[i]
		stats.functions++
		where := fmt.Sprintf("function %d (line %d)", i, p.LineDefined)
		if fe, ok := s.Node.(*ast.FunctionExpr); ok && p.LineDefined != fe.Line() {
			diffs = append(diffs, fmt.Sprintf("%s: scope's function is at line %d", where, fe.Line()))
			continue
		}
		// upvalues
		var ups []string
		for _, u := range s.Upvalues {
			ups = append(ups, u.Name)
			if !u.Captured {
				diffs = append(diffs, fmt.Sprintf("%s: upvalue %s of a variable not captured", where, u.Name))
			}
			if u.Scope.Func == s {
				diffs = append(diffs, fmt.Sprintf("%s: upvalue %s is its own variable", where, u.Name))
			}
		}
		if !equal(ups, p.DbgUpvalues) {
			diffs = append(diffs, fmt.Sprintf("%s: upvalues %v, compiler %v", where, ups, p.DbgUpvalues))
		}
		stats.upvalues += len(ups)
		// the operands of the instructions
		gets, sets := map[string]int{}, map[string]int{}
		upgets, upsets := map[string]int{}, map[string]int{}
		for pc := 0; pc < len(p.Code); pc++ {
			ins := p.Code[pc]
			op := int(ins >> 26)
			b := int(ins & 0x1ff)
			bx := int(ins & 0x3ffff)
			switch op {
			case lua.OP_GETGLOBAL:
				gets[string(p.Constants[bx].(lua.LString))]++
			case lua.OP_SETGLOBAL:
				sets[string(p.Constants[bx].(lua.LString))]++
			case lua.OP_GETUPVAL:
				upgets[p.DbgUpvalues[b]]++
			case lua.OP_SETUPVAL:
				upsets[p.DbgUpvalues[b]]++
			case lua.OP_CLOSURE:
				// the MOVE and GETUPVAL after it say what the closure captures
				pc += len(p.FunctionPrototypes[bx].DbgUpvalues)
			}
		}
		diffs = compareCounts(diffs, where, "global reads", globalReads[s], gets)
		diffs = compareCounts(diffs, where, "global writes", globalWrites[s], sets)
		diffs = compareCounts(diffs, where, "upvalue reads", upReads[s], upgets)
		diffs = compareCounts(diffs, where, "upvalue writes", upWrites[s], upsets)
		for _, n := range gets {
			stats.globalRefs += n
		}
		for _, n := range sets {
			stats.globalRefs += n
		}
		for _, n := range upgets {
			stats.upvalueRefs += n
		}
		for _, n := range upsets {
			stats.upvalueRefs += n
		}
		// locals
		var locals, dbg []string
		for _, v := range info.Vars {
			if v.Scope.Func == s && v.Kind != scope.KindLabel {
				locals = append(locals, v.Name)
			}
		}
		for _, l := range p.DbgLocals {
			if !strings.HasPrefix(l.Name, "(") {
				dbg = append(dbg, l.Name)
			}
		}
		sort.Strings(locals)
		sort.Strings(dbg)
		if !equal(locals, dbg) {
			diffs = append(diffs, fmt.Sprintf("%s: locals %v, compiler %v", where, locals, dbg))
		}
		stats.locals += len(locals)
	}
	return stats, diffs
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func compareCounts(diffs []string, where, what string, got, want map[string]int) []string {
	for name, n := range got {
		if want[name] != n {
			diffs = append(diffs, fmt.Sprintf("%s: %s of %q: %d, compiler %d", where, what, name, n, want[name]))
		}
	}
	for name, n := range want {
		if _, ok := got[name]; !ok {
			diffs = append(diffs, fmt.Sprintf("%s: %s of %q: 0, compiler %d", where, what, name, n))
		}
	}
	return diffs
}

// runOracle runs the compiler oracle over files in both dialects.
func runOracle(t *testing.T, label string, files map[string][]byte) {
	t.Helper()
	for _, d := range dialects {
		var total oracleStats
		failed := 0
		for _, name := range sortedKeys(files) {
			stats, diffs := compilerOracle(files[name], d)
			total.add(stats)
			if len(diffs) > 0 {
				failed++
				if failed <= 10 {
					t.Errorf("%s (%v): %d differences, first: %s", name, d, len(diffs), diffs[0])
				}
			}
		}
		t.Logf("%s, %v: %d sources (%d do not parse, %d do not compile), %d functions, %d upvalues, %d global refs, %d upvalue refs, %d locals compared; %d sources differ",
			label, d, total.sources, total.parseErrors, total.compileErrors, total.functions, total.upvalues, total.globalRefs, total.upvalueRefs, total.locals, failed)
	}
}

// TestOracleCompat runs the compiler oracle over the compatibility
// scripts and the other Lua files of the fork's testdata.
func TestOracleCompat(t *testing.T) {
	files := luaFiles(t, filepath.Join("..", "testdata"))
	if len(files) == 0 {
		t.Fatal("no scripts")
	}
	runOracle(t, "testdata", files)
}

// TestOracleArchives runs the compiler oracle over the Lua files of the
// luasuites archives.
func TestOracleArchives(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	runOracle(t, "archives", archiveFiles(t))
}
