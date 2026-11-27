package lua

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

/* load lib {{{ */

// go-moonscript patch: Lua 5.1's (and LuaJIT 2.1's) loaders: preload, Lua
// files on package.path, C libraries on package.cpath and the C root
// loader, which find what Lua's find and fail as Lua does without dynamic
// libraries (no C library can be loaded here).
var loLoaders = []LGFunction{loLoaderPreload, loLoaderLua, loLoaderC, loLoaderCroot}

func loGetPath(env string, defpath string) string {
	path := os.Getenv(env)
	if len(path) == 0 {
		path = defpath
	}
	path = strings.Replace(path, ";;", ";"+defpath+";", -1)
	if os.PathSeparator != '/' {
		dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
		if err != nil {
			panic(err)
		}
		path = strings.Replace(path, "!", dir, -1)
	}
	return path
}

// loPackage is the package table of require and the loaders (go-moonscript
// patch), as Lua 5.1's are the environment of its functions: the loaders'
// upvalue, set by OpenPackage, or the table package.loaded.package for
// require (a function of the base library here; the global package can be
// replaced).
func loPackage(L *LState) LValue {
	if L.currentFrame != nil && len(L.currentFrame.Fn.Upvalues) > 0 {
		if pkg, ok := L.Get(UpvalueIndex(1)).(*LTable); ok {
			return pkg
		}
	}
	return L.GetField(L.GetField(L.Get(RegistryIndex), "_LOADED"), LoadLibName)
}

// loFindFile is loadlib.c's findfile (go-moonscript patch): the first
// readable file of the templates of package[pname] for name, or "" and the
// "\n\tno file '...'" lines of those tried.
func loFindFile(L *LState, name, pname string) (string, string) {
	name = strings.Replace(name, ".", string(os.PathSeparator), -1)
	lv := L.GetField(loPackage(L), pname)
	if !LVCanConvToString(lv) {
		L.RaiseError("'package.%s' must be a string", pname)
	}
	var msg strings.Builder
	for _, pattern := range strings.Split(L.lvAsString(lv), LuaPathSep) {
		if pattern == "" {
			continue
		}
		luapath := strings.Replace(pattern, LuaPathMark, name, -1)
		if f, err := os.Open(luapath); err == nil { // readable(), as fopen
			_ = f.Close()
			return luapath, ""
		}
		fmt.Fprintf(&msg, "\n\tno file '%s'", luapath)
	}
	return "", msg.String()
}

// loLoadError is loadlib.c's loaderror.
func loLoadError(L *LState, name, filename, msg string) {
	L.RaiseError("error loading module '%s' from file '%s':\n\t%s", name, filename, msg)
}

// loDynamicLibraries is the error of loading a C library (loLoadLib's).
const loDynamicLibraries = "dynamic libraries not enabled; check your Lua installation"

var loopdetection = &LUserData{}

// loRequire is ll_require (go-moonscript patch): package.loaders read from
// the package table at each call, the loaders' messages concatenated in
// "module 'x' not found:", Lua 5.1's errors.
func loRequire(L *LState) int {
	name := L.CheckString(1)
	L.SetTop(1)
	loaded := L.GetField(L.Get(RegistryIndex), "_LOADED")
	if lv := L.GetField(loaded, name); LVAsBool(lv) {
		if lv == loopdetection {
			L.RaiseError("loop or previous error loading module '%s'", name)
		}
		L.Push(lv)
		return 1
	}
	loaders, ok := L.GetField(loPackage(L), "loaders").(*LTable)
	if !ok {
		L.RaiseError("'package.loaders' must be a table")
	}
	var msg strings.Builder
	var modasfunc LValue
	for i := 1; ; i++ {
		loader := loaders.RawGetInt(i)
		if loader == LNil {
			L.RaiseError("module '%s' not found:%s", name, msg.String())
		}
		L.Push(loader)
		L.Push(LString(name))
		L.Call(1, 1)
		ret := L.reg.Pop()
		if _, isFn := ret.(*LFunction); isFn {
			modasfunc = ret
			break
		}
		if ret.Type() == LTString || ret.Type() == LTNumber {
			msg.WriteString(L.lvAsString(ret))
		}
	}
	L.SetField(loaded, name, loopdetection)
	L.Push(modasfunc)
	L.Push(LString(name))
	L.Call(1, 1)
	if ret := L.reg.Pop(); ret != LNil {
		L.SetField(loaded, name, ret)
	}
	if L.GetField(loaded, name) == loopdetection {
		L.SetField(loaded, name, LTrue)
	}
	L.Push(L.GetField(loaded, name))
	return 1
}

func OpenPackage(L *LState) int {
	packagemod := L.RegisterModule(LoadLibName, loFuncs)

	L.SetField(packagemod, "preload", L.NewTable())

	// go-moonscript patch: the loaders have the package table as their
	// upvalue (Lua 5.1's environment of the package functions)
	loaders := L.CreateTable(len(loLoaders), 0)
	for i, loader := range loLoaders {
		L.RawSetInt(loaders, i+1, L.NewClosure(loader, packagemod))
	}
	L.SetField(packagemod, "loaders", loaders)
	L.SetField(L.Get(RegistryIndex), "_LOADERS", loaders)

	// go-moonscript patch: the registry's _LOADED, as Lua 5.1's
	// luaopen_package does (with package.loaded.package; it was a new table)
	loaded := L.FindTable(L.Get(RegistryIndex).(*LTable), "_LOADED", 1)
	L.SetField(packagemod, "loaded", loaded)
	L.SetField(L.Get(RegistryIndex), "_LOADED", loaded)

	L.SetField(packagemod, "path", LString(loGetPath(LuaPath, LuaPathDefault)))
	L.SetField(packagemod, "cpath", emptyLString)

	// go-moonscript patch: no "\n" after the last line, as in Lua 5.1
	L.SetField(packagemod, "config", LString(LuaDirSep+"\n"+LuaPathSep+
		"\n"+LuaPathMark+"\n"+LuaExecDir+"\n"+LuaIgMark))

	openLuaJIT(L, LoadLibName, packagemod) // go-moonscript patch (luajitlib.go)
	L.Push(packagemod)
	return 1
}

var loFuncs = map[string]LGFunction{
	"loadlib": loLoadLib,
	"seeall":  loSeeAll,
}

func loLoaderPreload(L *LState) int {
	name := L.CheckString(1)
	preload := L.GetField(loPackage(L), "preload") // go-moonscript patch
	if _, ok := preload.(*LTable); !ok {
		L.RaiseError("'package.preload' must be a table") // go-moonscript patch
	}
	lv := L.GetField(preload, name)
	if lv == LNil {
		// go-moonscript patch: the line of the "not found" message
		L.Push(LString(fmt.Sprintf("\n\tno field package.preload['%s']", name)))
		return 1
	}
	L.Push(lv)
	return 1
}

func loLoaderLua(L *LState) int {
	name := L.CheckString(1)
	path, msg := loFindFile(L, name, "path")
	if len(path) == 0 {
		L.Push(LString(msg))
		return 1
	}
	fn, err1 := L.loadFile(path, false)
	if err1 != nil {
		// go-moonscript patch: Lua 5.1's loaderror
		loLoadError(L, name, path, LVAsString(loadErrorValue(err1)))
	}
	L.Push(fn)
	return 1
}

// loLoaderC is loader_C (go-moonscript patch): a C library on
// package.cpath, which cannot be loaded.
func loLoaderC(L *LState) int {
	name := L.CheckString(1)
	path, msg := loFindFile(L, name, "cpath")
	if len(path) == 0 {
		L.Push(LString(msg))
		return 1
	}
	loLoadError(L, name, path, loDynamicLibraries)
	return 0
}

// loLoaderCroot is loader_Croot (go-moonscript patch): the C library of the
// root of a dotted name ("a" for "a.b") on package.cpath.
func loLoaderCroot(L *LState) int {
	name := L.CheckString(1)
	root, _, dotted := strings.Cut(name, ".")
	if !dotted {
		return 0
	}
	path, msg := loFindFile(L, root, "cpath")
	if len(path) == 0 {
		L.Push(LString(msg))
		return 1
	}
	loLoadError(L, name, path, loDynamicLibraries)
	return 0
}

func loSeeAll(L *LState) int {
	mod := L.CheckTable(1)
	mt := L.GetMetatable(mod)
	if mt == LNil {
		mt = L.CreateTable(0, 1)
		L.SetMetatable(mod, mt)
	}
	L.SetField(mt, "__index", L.Get(GlobalsIndex))
	return 0
}

/* }}} */

//
