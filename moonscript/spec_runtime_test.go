package moonscript

// Behavioral upstream specs, run in a Lua 5.1 VM (github.com/yuin/gopher-lua).
//
// The upstream v0.8.0 busted specs that execute compiled code
// (spec/class_spec.moon, comprehension_spec, destructure_spec, loops_spec,
// import_spec, moon_spec, correlate_spec, moonscript_spec) are checked in
// verbatim under testdata/upstream_spec/. Each one is compiled by this port,
// loaded into gopher-lua next to a small busted/luassert stand-in
// (testdata/upstream_spec/busted.lua) and every `it` block runs as a Go
// subtest:
//
//	go test -run 'UpstreamSpecs/class_spec' .
//
// The MoonScript modules the specs require are provided as:
//
//	moonscript.parse    Go-backed: parse.String (trees are opaque userdata)
//	moonscript.compile  Go-backed: compiler.Tree / compiler.FormatError
//	moonscript.util     upstream moonscript/util.moon compiled by this port
//	moonscript.base     upstream moonscript/base.moon compiled by this port
//	                    (to_lua, loadstring... on top of the Go modules)
//	moon                upstream moon/init.moon compiled by this port
//	spec.helpers        testdata/upstream_spec/helpers.lua (unindent, with_dev)
//
// so a failure can come from the compiled spec, a compiled library module or
// the Go modules; the error message points at the chunk.

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	lua "github.com/tarantool-contrib/go-moonscript/lua"

	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
	"github.com/tarantool-contrib/go-moonscript/moonscript/compiler"
	"github.com/tarantool-contrib/go-moonscript/moonscript/parse"
)

const upstreamSpecDir = "testdata/upstream_spec"

// specSkips lists upstream tests that can't run here, keyed by
// "<spec file> <full test name>".
var specSkips = map[string]string{}

func TestUpstreamSpecs(t *testing.T) {
	specs, _ := filepath.Glob(filepath.Join(upstreamSpecDir, "*_spec.moon"))
	if len(specs) == 0 {
		t.Fatal("no specs")
	}
	for _, spec := range specs {
		name := strings.TrimSuffix(filepath.Base(spec), ".moon")
		t.Run(name, func(t *testing.T) {
			runUpstreamSpec(t, spec)
		})
	}
}

func runUpstreamSpec(t *testing.T, spec string) {
	L := newSpecState(t)
	defer L.Close()

	busted := L.GetGlobal("__busted").(*lua.LTable)

	src, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	code, _, cerr := ToLua(string(src), Options{})
	if cerr != nil {
		t.Fatalf("compiling %s: %v", spec, cerr)
	}
	fn, lerr := L.Load(strings.NewReader(code), "@spec/"+filepath.Base(spec))
	if lerr != nil {
		t.Fatalf("loading compiled %s: %v", spec, lerr)
	}
	L.Push(fn)
	if err := L.PCall(0, 0, nil); err != nil {
		t.Fatalf("running %s: %v", spec, err)
	}

	count := callLua(t, L, busted.RawGetString("count"))[0]
	n := int(lua.LVAsNumber(count))
	if n == 0 {
		t.Fatal("spec registered no tests")
	}
	tests := busted.RawGetString("tests").(*lua.LTable)
	for i := 1; i <= n; i++ {
		testName := lua.LVAsString(tests.RawGetInt(i).(*lua.LTable).RawGetString("name"))
		t.Run(testName, func(t *testing.T) {
			if reason, ok := specSkips[filepath.Base(spec)+" "+testName]; ok {
				t.Skip(reason)
			}
			res := callLua(t, L, busted.RawGetString("run"), lua.LNumber(i))
			switch {
			case res[1] == lua.LString("pending"):
				t.Skip("pending upstream")
			case res[1] != lua.LTrue:
				t.Errorf("%s", lua.LVAsString(res[2]))
			}
		})
	}
}

// callLua calls fn with args and returns its results (padded to 3).
func callLua(t *testing.T, L *lua.LState, fn lua.LValue, args ...lua.LValue) []lua.LValue {
	t.Helper()
	top := L.GetTop()
	L.Push(fn)
	for _, a := range args {
		L.Push(a)
	}
	if err := L.PCall(len(args), lua.MultRet, nil); err != nil {
		t.Fatal(err)
	}
	var out []lua.LValue
	for i := top + 1; i <= L.GetTop(); i++ {
		out = append(out, L.Get(i))
	}
	L.SetTop(top)
	for len(out) < 3 {
		out = append(out, lua.LNil)
	}
	return out
}

// newSpecState is a Lua state with the busted stand-in loaded (as global
// __busted) and the MoonScript modules preloaded.
func newSpecState(t *testing.T) *lua.LState {
	t.Helper()
	L := lua.NewState()

	L.PreloadModule("moonscript.parse", luaParseModule)
	L.PreloadModule("moonscript.compile", luaCompileModule)
	L.PreloadModule("moonscript.line_tables", func(L *lua.LState) int {
		L.Push(L.NewTable())
		return 1
	})

	libs, _ := filepath.Glob(filepath.Join(upstreamSpecDir, "lib", "*.moon"))
	for _, lib := range libs {
		mod := strings.TrimSuffix(filepath.Base(lib), ".moon")
		L.PreloadModule(mod, moonModuleLoader(lib, mod))
	}
	L.PreloadModule("spec.helpers", luaFileLoader(filepath.Join(upstreamSpecDir, "helpers.lua")))

	if err := L.DoFile(filepath.Join(upstreamSpecDir, "busted.lua")); err != nil {
		t.Fatal(err)
	}
	L.SetGlobal("__busted", L.Get(-1))
	L.Pop(1)
	return L
}

// moonModuleLoader compiles a MoonScript module with this port.
func moonModuleLoader(path, mod string) lua.LGFunction {
	return func(L *lua.LState) int {
		src, err := os.ReadFile(path)
		if err != nil {
			L.RaiseError("%v", err)
		}
		code, _, cerr := ToLua(string(src), Options{})
		if cerr != nil {
			L.RaiseError("compiling %s: %v", path, cerr)
		}
		fn, lerr := L.Load(strings.NewReader(code), "@"+strings.ReplaceAll(mod, ".", "/")+".lua")
		if lerr != nil {
			L.RaiseError("loading compiled %s: %v", path, lerr)
		}
		L.Push(fn)
		L.Call(0, 1)
		return 1
	}
}

func luaFileLoader(path string) lua.LGFunction {
	return func(L *lua.LState) int {
		fn, err := L.LoadFile(path)
		if err != nil {
			L.RaiseError("%v", err)
		}
		L.Push(fn)
		L.Call(0, 1)
		return 1
	}
}

const luaTreeType = "moonscript.tree"

// luaParseModule is moonscript.parse: string(code) -> tree | nil, err
func luaParseModule(L *lua.LState) int {
	mod := L.NewTable()
	L.SetField(mod, "string", L.NewFunction(func(L *lua.LState) int {
		tree, err := parse.String(L.CheckString(1))
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		ud := L.NewUserData()
		ud.Value = tree
		L.SetMetatable(ud, L.NewTypeMetatable(luaTreeType))
		L.Push(ud)
		return 1
	}))
	L.Push(mod)
	return 1
}

// luaCompileModule is moonscript.compile: tree(tree, options) -> code, posmap
// | nil, err, pos and format_error(msg, pos, code).
func luaCompileModule(L *lua.LState) int {
	mod := L.NewTable()
	L.SetField(mod, "tree", L.NewFunction(func(L *lua.LState) int {
		ud := L.CheckUserData(1)
		tree, ok := ud.Value.(*ast.Node)
		if !ok {
			L.ArgError(1, "expected a parsed tree")
		}
		var opts compiler.Options
		if o, ok := L.Get(2).(*lua.LTable); ok {
			if v := o.RawGetString("implicitly_return_root"); v == lua.LFalse {
				opts.NoImplicitReturnRoot = true
			}
			opts.Correlate = lua.LVAsBool(o.RawGetString("correlate"))
			if s, ok := o.RawGetString("source").(lua.LString); ok {
				opts.Source = string(s)
			}
		}
		code, posmap, cerr := compiler.Tree(tree, opts)
		if cerr != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(cerr.Msg))
			if cerr.Pos != 0 {
				L.Push(lua.LNumber(cerr.Pos))
			} else {
				L.Push(lua.LNil)
			}
			return 3
		}
		pm := L.NewTable()
		for line, pos := range posmap {
			pm.RawSetInt(line, lua.LNumber(pos))
		}
		L.Push(lua.LString(code))
		L.Push(pm)
		return 2
	}))
	L.SetField(mod, "format_error", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(compiler.FormatError(L.CheckString(1), L.OptInt(2, 0), L.CheckString(3))))
		return 1
	}))
	L.Push(mod)
	return 1
}

// TestBustedShim checks that the busted stand-in reports failures, so a
// passing upstream spec means its assertions really held.
func TestBustedShim(t *testing.T) {
	L := newSpecState(t)
	defer L.Close()
	err := L.DoString(`
describe("fail", function()
  it("same", function() assert.same({1, {a = 2}}, {1, {a = 3}}) end)
  it("same extra key", function() assert.same({1}, {1, x = 1}) end)
  it("equal", function() assert.equal({}, {}) end)
  it("is_true", function() assert.is_true(1) end)
  it("is_false", function() assert.is_false(nil) end)
  it("is_nil", function() assert.is_nil(false) end)
  it("truthy", function() assert.truthy(nil) end)
  it("falsy", function() assert.falsy(0) end)
  it("matches", function() assert.matches("^x", "yx") end)
  it("has_error none", function() assert.has_error(function() end) end)
  it("has_error message", function() assert.has_error(function() error("a") end, "b") end)
  it("spy", function() local s = spy.on({}, "f"); assert.spy(s).was.called() end)
  it("call", function() assert(false, "nope") end)
end)
describe("pass", function()
  local n = 0
  setup(function() n = n + 1 end)
  before_each(function() n = n + 10 end)
  it("same", function() assert.same({1, {a = 2}}, {1, {a = 2}}) end)
  it("has_error message", function() assert.has_error(function() error("boom") end, "boom") end)
  it("spy", function() local t = {f = function() end}; spy.on(t, "f"); t.f(); assert.spy(t.f).was.called() end)
  it("hooks", function() assert.same(41, n) end)
end)
`)
	if err != nil {
		t.Fatal(err)
	}
	busted := L.GetGlobal("__busted").(*lua.LTable)
	n := int(lua.LVAsNumber(callLua(t, L, busted.RawGetString("count"))[0]))
	if n != 17 {
		t.Fatalf("registered %d tests, want 17", n)
	}
	for i := 1; i <= n; i++ {
		res := callLua(t, L, busted.RawGetString("run"), lua.LNumber(i))
		name, ok := lua.LVAsString(res[0]), res[1] == lua.LTrue
		wantOK := strings.HasPrefix(name, "pass ")
		if ok != wantOK {
			t.Errorf("%s: ok = %v (%s)", name, ok, lua.LVAsString(res[2]))
		}
	}
}

// TestUpstreamSpecSources compares the compiled spec files and library
// modules TestUpstreamSpecs runs against the reference compiler's output
// (testdata/upstream_spec/ref/<name>.lua, moonscript.base.to_lua), so a
// behavioral pass can't hide a textual difference.
func TestUpstreamSpecSources(t *testing.T) {
	inputs, _ := filepath.Glob(filepath.Join(upstreamSpecDir, "*.moon"))
	libs, _ := filepath.Glob(filepath.Join(upstreamSpecDir, "lib", "*.moon"))
	inputs = append(inputs, libs...)
	for _, in := range inputs {
		name := strings.TrimSuffix(filepath.Base(in), ".moon")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(upstreamSpecDir, "ref", name+".lua"))
			if err != nil {
				t.Fatal(err)
			}
			code, _, cerr := ToLua(string(src), Options{})
			if cerr != nil {
				t.Fatal(cerr)
			}
			if code != string(want) {
				t.Errorf("output mismatch\n%s", firstDiff(string(want), code))
			}
		})
	}
}

// TestUpstreamSpecSnippets replays every compile the runtime specs perform
// (correlate_spec and destructure_spec compile snippets through
// moonscript.base.to_lua / moonscript.compile.tree) and compares code and
// posmap with what the reference produced for the same calls when running
// the same specs (testdata/upstream_spec/ref/runtime_compiles.json, recorded
// by scripts/refspecrun.lua).
func TestUpstreamSpecSnippets(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(upstreamSpecDir, "ref", "runtime_compiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recs []struct {
		Spec, Test string
		Compiles   []struct {
			Src                  string
			Correlate            bool
			ImplicitlyReturnRoot bool `json:"implicitly_return_root"`
			Code                 *string
			Posmap               map[string]int
			Err                  *string
			Pos                  int
		}
	}
	if err := json.Unmarshal(data, &recs); err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("no records")
	}
	for _, rec := range recs {
		t.Run(rec.Spec+"/"+rec.Test, func(t *testing.T) {
			for i, c := range rec.Compiles {
				tree, perr := parse.String(c.Src)
				if perr != nil {
					t.Fatalf("compile %d: parse: %v", i+1, perr)
				}
				code, posmap, cerr := compiler.Tree(tree, compiler.Options{
					NoImplicitReturnRoot: !c.ImplicitlyReturnRoot,
					Correlate:            c.Correlate,
					Source:               c.Src,
				})
				if c.Err != nil {
					if cerr == nil || cerr.Msg != *c.Err || cerr.Pos != c.Pos {
						t.Errorf("compile %d: want error %q at %d, got %v", i+1, *c.Err, c.Pos, cerr)
					}
					continue
				}
				if cerr != nil {
					t.Fatalf("compile %d: %v\nsource:\n%s", i+1, cerr, c.Src)
				}
				if code != *c.Code {
					t.Errorf("compile %d (correlate=%v): output mismatch\nsource:\n%s\n%s", i+1, c.Correlate, c.Src, firstDiff(*c.Code, code))
				}
				want := map[int]int{}
				for k, v := range c.Posmap {
					n, _ := strconv.Atoi(k)
					want[n] = v
				}
				if !maps.Equal(want, posmap) {
					t.Errorf("compile %d: posmap mismatch\nwant %v\ngot  %v", i+1, want, posmap)
				}
			}
		})
	}
}
