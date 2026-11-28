// Package runtime runs MoonScript on gopher-lua (github.com/yuin/gopher-lua,
// a Lua 5.1 VM in Go): it is the Go counterpart of the MoonScript v0.8.0
// Lua runtime modules, the support code behind the moon command.
//
// Open (or NewState) makes these modules available to require:
//
//	moonscript             moonscript/init.moon: moonscript.base after insert_loader!
//	moonscript.base        to_lua, loadstring, loadfile, dofile, moon_loader,
//	                       insert_loader, remove_loader, create_moonpath, dirsep
//	moonscript.parse       Go-backed: string(code) -> tree | nil, err
//	                       (trees are opaque userdata, only good for compile.tree)
//	moonscript.compile     Go-backed: tree(tree, options) -> code, line table |
//	                       nil, err, pos; format_error(err, pos, code)
//	moonscript.errors      rewrite_traceback, truncate_traceback,
//	                       reverse_line_number, user_error
//	moonscript.line_tables chunk name -> line table, filled by loadstring
//	moonscript.util        moonscript/util.moon
//	moonscript.data        moonscript/data.moon
//	moonscript.version     moonscript/version.moon
//	moonscript.cmd.coverage
//	                       moonscript/cmd/coverage.moon (moon --coverage)
//	moon, moon.all         the standard library (moon/init.moon, moon/all.moon)
//
// The pure-Lua modules are the upstream .moon sources (runtime/moonsrc)
// compiled by this port at `go generate` time and embedded (runtime/lua);
// they are byte-identical to the reference compiler's output, except
// moonscript.errors, whose lpeg grammar is replaced by plain Lua (see the
// note in moonsrc/moonscript/errors.moon). The .moon loader
// (moonscript.base.moon_loader) uses package.moonpath, which insert_loader
// derives from package.path with create_moonpath.
//
// Open also applies Lua 5.1 compatibility patches to gopher-lua (OpenCompat;
// see compat.go) so that chunk names, error positions and messages look like
// the reference interpreter's — moonscript.errors depends on that format.
//
// gopher-lua v1.1.2 (and its master branch) closes the open upvalues of all
// live frames whenever an error is raised under pcall, so closures stop
// sharing locals with the frames that survive the error (and the moon
// driver loses the error its xpcall handler stored). This module therefore
// carries a patched copy as one of its packages,
// github.com/tarantool-contrib/go-moonscript/lua
// (lua/moonscript.patch is the change); the *lua.LState
// and other Lua types of this package's API are that package's, not
// github.com/yuin/gopher-lua's.
//
// The patched gopher-lua also has debug.sethook and debug.gethook with Lua
// 5.1 semantics (lua/hook.go), which
// moonscript.cmd.coverage uses. That module deliberately differs from
// upstream 0.8.0, whose hook is a tail call and never records anything on
// Lua 5.1: its hook is not a tail call, and its report lists files sorted
// and skips sources that aren't files.
//
// Not provided: moonscript.dump, moonscript.transform, moonscript.types and
// the other moonscript.cmd.* modules (parse trees are not Lua tables here),
// and lpeg.
package runtime

//go:generate go run ../internal/tools/genruntime -root ..

import (
	"embed"
	"errors"
	"fmt"
	"strings"

	lua "github.com/tarantool-contrib/go-moonscript/lua"

	"github.com/tarantool-contrib/go-moonscript/ast"
	"github.com/tarantool-contrib/go-moonscript/compiler"
	"github.com/tarantool-contrib/go-moonscript/parse"
)

// Version is the MoonScript version this runtime implements.
const Version = "0.8.0"

//go:embed lua
var luaFiles embed.FS

// embedded lists the precompiled modules: module name -> file in luaFiles.
var embedded = map[string]string{
	"moonscript":              "lua/moonscript/init.lua",
	"moonscript.base":         "lua/moonscript/base.lua",
	"moonscript.data":         "lua/moonscript/data.lua",
	"moonscript.errors":       "lua/moonscript/errors.lua",
	"moonscript.line_tables":  "lua/moonscript/line_tables.lua",
	"moonscript.util":         "lua/moonscript/util.lua",
	"moonscript.version":      "lua/moonscript/version.lua",
	"moonscript.cmd.coverage": "lua/moonscript/cmd/coverage.lua",
	"moon":                    "lua/moon/init.lua",
	"moon.all":                "lua/moon/all.lua",
}

// Modules returns the names of the modules Open preloads.
func Modules() []string {
	names := []string{"moonscript.parse", "moonscript.compile"}
	for name := range embedded {
		names = append(names, name)
	}
	return names
}

// Lua 5.1 limits: LUAI_MAXCALLS nested calls; the data stack may grow to
// what such a call depth needs.
const (
	maxCalls        = 20000
	maxRegistrySize = 1 << 22
)

// NewState returns a Lua state with the standard libraries and MoonScript
// support (Open). Without options, it is StateOptions(lua.DialectLua51)'s
// state: PUC Lua 5.1, with the call stack and data stack limits raised to
// Lua 5.1's (gopher-lua defaults to 256 nested calls) and the stacks
// growing on demand. The options, if given, are used as they are (but
// the standard libraries are always opened); for the LuaJIT 2.1 dialect
// with the default limits, pass StateOptions(lua.DialectLuaJIT).
func NewState(opts ...lua.Options) *lua.LState {
	o := StateOptions(lua.DialectLua51)
	if len(opts) > 0 {
		o = opts[0]
	}
	o.SkipOpenLibs = false
	L := lua.NewState(o)
	Open(L)
	return L
}

// StateOptions returns the options NewState uses by default, with the
// given dialect: lua.DialectLua51 (PUC Lua 5.1.5, the reference moon's
// interpreter) or lua.DialectLuaJIT (LuaJIT 2.1: its library extensions,
// the bit library and a jit module; see the gopher-lua copy's
// luajitlib.go).
func StateOptions(dialect lua.Dialect) lua.Options {
	return lua.Options{
		CallStackSize:       maxCalls,
		MinimizeStackMemory: true,
		RegistrySize:        lua.RegistrySize,
		RegistryMaxSize:     maxRegistrySize,
		RegistryGrowStep:    1024,
		Dialect:             dialect,
	}
}

// ParseDialect returns the dialect a name selects: "lua51" (also "5.1",
// "lua5.1", "lua", "puc" and "", the default) or "luajit" (also "jit",
// "luajit2.1"), case-insensitively.
func ParseDialect(name string) (lua.Dialect, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "lua51", "lua5.1", "5.1", "lua", "puc":
		return lua.DialectLua51, nil
	case "luajit", "jit", "luajit2.1", "luajit21":
		return lua.DialectLuaJIT, nil
	}
	return lua.DialectLua51, fmt.Errorf("unknown Lua dialect %q (lua51 or luajit)", name)
}

// Open installs MoonScript support into L, which must have the standard
// libraries open: the Lua 5.1 compatibility patches (OpenCompat) and the
// modules listed in the package documentation, registered in
// package.preload. Like the reference runtime, it does not insert the .moon
// loader; requiring "moonscript" (or calling InsertLoader) does.
func Open(L *lua.LState) {
	OpenCompat(L)
	L.PreloadModule("moonscript.parse", openParse)
	L.PreloadModule("moonscript.compile", openCompile)
	for name, file := range embedded {
		L.PreloadModule(name, embeddedLoader(file))
	}
}

// embeddedLoader loads a precompiled module, chunk name "@<path>" with the
// path relative to a Lua module directory (e.g. "@moonscript/base.lua").
func embeddedLoader(file string) lua.LGFunction {
	return func(L *lua.LState) int {
		code, err := luaFiles.ReadFile(file)
		if err != nil {
			L.RaiseError("%v", err)
		}
		fn, err := loadLua(L, string(code), "@"+strings.TrimPrefix(file, "lua/"))
		if err != nil {
			L.RaiseError("%v", err)
		}
		L.Push(fn)
		L.Push(L.Get(1))
		L.Call(1, 1)
		return 1
	}
}

const treeTypeName = "moonscript.tree"

// openParse is moonscript.parse: string(code) -> tree | nil, err. The tree
// is a userdata wrapping the port's syntax tree.
func openParse(L *lua.LState) int {
	mod := L.NewTable()
	mod.RawSetString("string", L.NewFunction(func(L *lua.LState) int {
		tree, err := parse.String(L.CheckString(1))
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		ud := L.NewUserData()
		ud.Value = tree
		L.SetMetatable(ud, L.NewTypeMetatable(treeTypeName))
		L.Push(ud)
		return 1
	}))
	L.Push(mod)
	return 1
}

// openCompile is moonscript.compile: tree(tree, options) -> code, line
// table | nil, err, pos and format_error(err, pos, code).
func openCompile(L *lua.LState) int {
	mod := L.NewTable()
	mod.RawSetString("tree", L.NewFunction(func(L *lua.LState) int {
		ud := L.CheckUserData(1)
		tree, ok := ud.Value.(*ast.Node)
		if !ok {
			L.ArgError(1, "expected a tree from moonscript.parse")
		}
		code, posmap, cerr := compiler.Tree(tree, optionsFromLua(L.Get(2)))
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
		L.Push(lua.LString(code))
		L.Push(lineTableToLua(L, posmap))
		return 2
	}))
	mod.RawSetString("format_error", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(compiler.FormatError(L.CheckString(1), L.OptInt(2, 0), L.CheckString(3))))
		return 1
	}))
	L.Push(mod)
	return 1
}

// optionsFromLua reads compile options from a Lua options table.
func optionsFromLua(v lua.LValue) compiler.Options {
	var opts compiler.Options
	t, ok := v.(*lua.LTable)
	if !ok {
		return opts
	}
	if t.RawGetString("implicitly_return_root") == lua.LFalse {
		opts.NoImplicitReturnRoot = true
	}
	opts.Correlate = lua.LVAsBool(t.RawGetString("correlate"))
	if s, ok := t.RawGetString("source").(lua.LString); ok {
		opts.Source = string(s)
	}
	return opts
}

// optionsToLua is the inverse of optionsFromLua.
func optionsToLua(L *lua.LState, opts compiler.Options) *lua.LTable {
	t := L.NewTable()
	if opts.NoImplicitReturnRoot {
		t.RawSetString("implicitly_return_root", lua.LFalse)
	}
	if opts.Correlate {
		t.RawSetString("correlate", lua.LTrue)
	}
	if opts.Source != "" {
		t.RawSetString("source", lua.LString(opts.Source))
	}
	return t
}

func lineTableToLua(L *lua.LState, posmap map[int]int) *lua.LTable {
	t := L.NewTable()
	for line, pos := range posmap {
		t.RawSetInt(line, lua.LNumber(pos))
	}
	return t
}

// Options are compile options for LoadString and LoadFile.
type Options = compiler.Options

// require returns require(name), raising on failure.
func require(L *lua.LState, name string) (*lua.LTable, error) {
	res, err := call(L, L.GetGlobal("require"), 1, lua.LString(name))
	if err != nil {
		return nil, err
	}
	t, ok := res[0].(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("require %q: got a %s", name, res[0].Type())
	}
	return t, nil
}

// call calls fn in protected mode and returns nret results.
func call(L *lua.LState, fn lua.LValue, nret int, args ...lua.LValue) ([]lua.LValue, error) {
	top := L.GetTop()
	err := L.CallByParam(lua.P{Fn: fn, NRet: nret, Protect: true}, args...)
	if err != nil {
		L.SetTop(top)
		return nil, err
	}
	res := make([]lua.LValue, nret)
	for i := range res {
		res[i] = L.Get(top + 1 + i)
	}
	L.SetTop(top)
	return res, nil
}

// field calls module.fn(args...).
func field(L *lua.LState, module, fn string, nret int, args ...lua.LValue) ([]lua.LValue, error) {
	mod, err := require(L, module)
	if err != nil {
		return nil, err
	}
	return call(L, mod.RawGetString(fn), nret, args...)
}

// LoadString is moonscript.base.loadstring: it compiles MoonScript code
// as chunk chunkname ("" means "=(moonscript.loadstring)"), records its line
// table in moonscript.line_tables and returns the Lua function.
func LoadString(L *lua.LState, code, chunkname string, opts Options) (*lua.LFunction, error) {
	args := []lua.LValue{lua.LString(code)}
	if chunkname != "" {
		args = append(args, lua.LString(chunkname))
	}
	args = append(args, optionsToLua(L, opts))
	return loadResult(field(L, "moonscript.base", "loadstring", 2, args...))
}

// LoadFile is moonscript.base.loadfile (chunk name "@" + path).
func LoadFile(L *lua.LState, path string, opts Options) (*lua.LFunction, error) {
	return loadResult(field(L, "moonscript.base", "loadfile", 2, lua.LString(path), optionsToLua(L, opts)))
}

func loadResult(res []lua.LValue, err error) (*lua.LFunction, error) {
	if err != nil {
		return nil, err
	}
	if fn, ok := res[0].(*lua.LFunction); ok {
		return fn, nil
	}
	return nil, errors.New(lua.LVAsString(res[1]))
}

// DoFile loads a MoonScript file with LoadFile and runs it, returning its
// results.
func DoFile(L *lua.LState, path string) ([]lua.LValue, error) {
	fn, err := LoadFile(L, path, Options{})
	if err != nil {
		return nil, err
	}
	top := L.GetTop()
	L.Push(fn)
	if err := L.PCall(0, lua.MultRet, nil); err != nil {
		return nil, err
	}
	res := make([]lua.LValue, 0, L.GetTop()-top)
	for i := top + 1; i <= L.GetTop(); i++ {
		res = append(res, L.Get(i))
	}
	L.SetTop(top)
	return res, nil
}

// InsertLoader is moonscript.base.insert_loader: it sets package.moonpath
// (if unset) and inserts the .moon loader into package.loaders at
// position 2. It reports false if the loader was already installed.
func InsertLoader(L *lua.LState) (bool, error) {
	res, err := field(L, "moonscript.base", "insert_loader", 1)
	if err != nil {
		return false, err
	}
	return lua.LVAsBool(res[0]), nil
}

// RemoveLoader is moonscript.base.remove_loader.
func RemoveLoader(L *lua.LState) (bool, error) {
	res, err := field(L, "moonscript.base", "remove_loader", 1)
	if err != nil {
		return false, err
	}
	return lua.LVAsBool(res[0]), nil
}

// CreateMoonpath is moonscript.base.create_moonpath: package.path with each
// "<prefix>.lua" template turned into "<prefix>.moon" and the others dropped.
func CreateMoonpath(L *lua.LState, packagePath string) (string, error) {
	res, err := field(L, "moonscript.base", "create_moonpath", 1, lua.LString(packagePath))
	if err != nil {
		return "", err
	}
	return lua.LVAsString(res[0]), nil
}

// LineTable returns the line table moonscript.base.loadstring recorded for
// chunkname (e.g. "@foo.moon"): Lua line -> 1-based MoonScript byte offset.
// It is nil if there is none.
func LineTable(L *lua.LState, chunkname string) (map[int]int, error) {
	tables, err := require(L, "moonscript.line_tables")
	if err != nil {
		return nil, err
	}
	t, ok := tables.RawGetString(chunkname).(*lua.LTable)
	if !ok {
		return nil, nil
	}
	out := map[int]int{}
	t.ForEach(func(k, v lua.LValue) {
		kn, ok1 := k.(lua.LNumber)
		vn, ok2 := v.(lua.LNumber)
		if ok1 && ok2 {
			out[int(kn)] = int(vn)
		}
	})
	return out, nil
}

// ReverseLineNumber is moonscript.errors.reverse_line_number: the
// MoonScript line of Lua line luaLine in file fname, given the file's line
// table; ok is false when the reference returns "unknown".
func ReverseLineNumber(L *lua.LState, fname string, lineTable map[int]int, luaLine int) (line int, ok bool, err error) {
	res, err := field(L, "moonscript.errors", "reverse_line_number", 1,
		lua.LString(fname), lineTableToLua(L, lineTable), lua.LNumber(luaLine), L.NewTable())
	if err != nil {
		return 0, false, err
	}
	n, ok := res[0].(lua.LNumber)
	return int(n), ok, nil
}

// TruncateTraceback is moonscript.errors.truncate_traceback: it drops the
// traceback lines below the last one mentioning chunkFunc ("" means
// "moonscript_chunk") and calls that frame "main chunk".
func TruncateTraceback(L *lua.LState, traceback, chunkFunc string) (string, error) {
	args := []lua.LValue{lua.LString(traceback)}
	if chunkFunc != "" {
		args = append(args, lua.LString(chunkFunc))
	}
	res, err := field(L, "moonscript.errors", "truncate_traceback", 1, args...)
	if err != nil {
		return "", err
	}
	return lua.LVAsString(res[0]), nil
}

// RewriteTraceback is moonscript.errors.rewrite_traceback: given a
// traceback ("stack traceback:\n\t...") and an error message, it maps every
// "<file>:<lua line>:" position of a chunk with a line table to
// "<file>:<moon line>: (<lua line>)" and returns "moon: <err>\n<traceback>".
// ok is false when the traceback isn't in the expected format.
func RewriteTraceback(L *lua.LState, traceback, errMsg string) (string, bool, error) {
	res, err := field(L, "moonscript.errors", "rewrite_traceback", 1, lua.LString(traceback), lua.LString(errMsg))
	if err != nil {
		return "", false, err
	}
	s, ok := res[0].(lua.LString)
	return string(s), ok, nil
}
