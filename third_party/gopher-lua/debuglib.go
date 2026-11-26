package lua

import (
	"io"
	"os"
	"strings"
)

func OpenDebug(L *LState) int {
	dbgmod := L.RegisterModule(DebugLibName, debugFuncs)
	openLuaJIT(L, DebugLibName, dbgmod) // go-moonscript patch (luajitlib.go)
	L.Push(dbgmod)
	return 1
}

var debugFuncs = map[string]LGFunction{
	"debug":        debugDebug,       // go-moonscript patch
	"getregistry":  debugGetRegistry, // go-moonscript patch
	"getfenv":      debugGetFEnv,
	"gethook":      debugGetHook, // go-moonscript patch (hook.go)
	"getinfo":      debugGetInfo,
	"getlocal":     debugGetLocal,
	"getmetatable": debugGetMetatable,
	"getupvalue":   debugGetUpvalue,
	"setfenv":      debugSetFEnv,
	"sethook":      debugSetHook, // go-moonscript patch (hook.go)
	"setlocal":     debugSetLocal,
	"setmetatable": debugSetMetatable,
	"setupvalue":   debugSetUpvalue,
	"traceback":    debugTraceback,
}

func debugGetFEnv(L *LState) int {
	L.Push(L.GetFEnv(L.CheckAny(1)))
	return 1
}

// go-moonscript patch: debug.getinfo is Lua 5.1's db_getinfo: an optional
// thread first, options "flnSu" by default, an error for other values than
// a function or a level and for an invalid option, and the fields of
// lua_getinfo: short_src, what "C" for Go functions (GetInfo), activelines
// ('L'), and a level lost to a tail call as info_tailcall gives it.
func debugGetInfo(L *LState) int {
	L1, arg := L, 0
	if th, ok := L.Get(1).(*LState); ok {
		L1, arg = th, 1
	}
	what := L.OptString(arg+2, "flnSu")
	var dbg *Debug
	var fn LValue
	var err error
	switch lv := L.Get(arg + 1).(type) {
	case *LFunction:
		dbg = &Debug{}
		fn, err = L1.GetInfo(">"+strings.ReplaceAll(what, "L", ""), dbg, lv)
		fn = lv
	case LNumber, LString:
		level, isNum := argNumber(lv)
		if !isNum {
			L.ArgError(arg+1, "function or level expected")
		}
		var yield *LFunction
		var ok bool
		dbg, yield, ok = threadStack(L, L1, int(level))
		if !ok {
			L.Push(LNil)
			return 1
		}
		if yield != nil {
			dbg = &Debug{}
			fn, err = L1.GetInfo(">"+strings.ReplaceAll(what, "L", ""), dbg, yield)
			fn = yield
			break
		}
		if L1.isTailCallLevel(int(level) - threadLevelOffset(L, L1)) {
			for _, c := range what {
				if !strings.ContainsRune("SlunfL", c) {
					L.ArgError(arg+2, "invalid option")
				}
			}
			tbl := L.NewTable()
			for _, c := range what {
				switch c {
				case 'S':
					tbl.RawSetString("source", LString("=(tail call)"))
					tbl.RawSetString("short_src", LString("(tail call)"))
					tbl.RawSetString("linedefined", LNumber(-1))
					tbl.RawSetString("lastlinedefined", LNumber(-1))
					tbl.RawSetString("what", LString("tail"))
				case 'l':
					tbl.RawSetString("currentline", LNumber(-1))
				case 'u':
					tbl.RawSetString("nups", LNumber(0))
				}
			}
			L.Push(tbl)
			return 1
		}
		fn, err = L1.GetInfo(strings.ReplaceAll(what, "L", "")+"f", dbg, LNil)
	default:
		L.ArgError(arg+1, "function or level expected")
	}

	if err != nil {
		L.ArgError(arg+2, "invalid option")
	}
	// only the fields what asks for, as in Lua 5.1 (this also makes the
	// calls debug hooks make cheaper)
	tbl := L.CreateTable(0, 8)
	if strings.ContainsRune(what, 'S') {
		tbl.RawSetString("source", LString(dbg.Source))
		tbl.RawSetString("short_src", LString(shortSource(dbg.Source)))
		tbl.RawSetString("linedefined", LNumber(dbg.LineDefined))
		tbl.RawSetString("lastlinedefined", LNumber(dbg.LastLineDefined))
		tbl.RawSetString("what", LString(dbg.What))
	}
	if strings.ContainsRune(what, 'l') {
		tbl.RawSetString("currentline", LNumber(dbg.CurrentLine))
	}
	if strings.ContainsRune(what, 'u') {
		tbl.RawSetString("nups", LNumber(dbg.NUpvalues))
	}
	if strings.ContainsRune(what, 'n') {
		tbl.RawSetString("namewhat", LString(dbg.NameWhat))
		if len(dbg.NameWhat) > 0 { // go-moonscript patch: and namewhat, as in Lua 5.1
			tbl.RawSetString("name", LString(dbg.Name))
		}
	}
	if strings.ContainsRune(what, 'L') {
		tbl.RawSetString("activelines", activeLines(L, fn))
	}
	if strings.ContainsRune(what, 'f') {
		tbl.RawSetString("func", fn)
	}
	L.Push(tbl)
	return 1
}

// shortSource is luaO_chunkid for gopher-lua's chunk names, which are
// display names already ("foo.lua", `[string "..."]`): only a '=' or '@'
// in front goes.
func shortSource(source string) string {
	if strings.HasPrefix(source, "=") || strings.HasPrefix(source, "@") {
		return source[1:]
	}
	return source
}

// activeLines is collectvalidlines: the lines of fn's instructions, as
// keys of a table, nil for a Go function.
func activeLines(L *LState, fn LValue) LValue {
	f, ok := fn.(*LFunction)
	if !ok || f.IsG || f.Proto == nil {
		return LNil
	}
	tbl := L.NewTable()
	for _, line := range f.Proto.DbgSourcePositions {
		tbl.RawSetInt(line, LTrue)
	}
	return tbl
}

func debugGetLocal(L *LState) int {
	// go-moonscript patch: an optional thread first, as in Lua 5.1
	L1, arg := L, 0
	if th, ok := L.Get(1).(*LState); ok {
		L1, arg = th, 1
	}
	if fn, ok := L.Get(arg + 1).(*LFunction); ok && L.Dialect() == DialectLuaJIT {
		// LuaJIT: the name of a parameter of a function
		idx := L.CheckInt(arg + 2)
		if name, ok := fn.LocalName(idx, 0); ok && !fn.IsG && idx <= int(fn.Proto.NumParameters) {
			L.Push(LString(name))
		} else {
			L.Push(LNil)
		}
		return 1
	}
	level := L.CheckInt(arg + 1)
	idx := L.CheckInt(arg + 2)
	dbg, yield, ok := threadStack(L, L1, level)
	if !ok {
		L.ArgError(arg+1, "level out of range")
	}
	if yield != nil {
		L.Push(LNil)
		return 1
	}
	name, value := L1.GetLocal(dbg, idx)
	if len(name) > 0 {
		L.Push(LString(name))
		L.Push(value)
		return 2
	}
	L.Push(LNil)
	return 1
}

func debugGetMetatable(L *LState) int {
	L.Push(L.GetMetatable(L.CheckAny(1)))
	return 1
}

func debugGetUpvalue(L *LState) int {
	fn := L.CheckFunction(1)
	idx := L.CheckInt(2)
	name, value := L.GetUpvalue(fn, idx)
	if len(name) > 0 {
		L.Push(LString(name))
		L.Push(value)
		return 2
	}
	L.Push(LNil)
	return 1
}

// go-moonscript patch: debug.setfenv is db_setfenv: a table environment,
// the object returned, an error for an object without one.
func debugSetFEnv(L *LState) int {
	L.CheckTable(2)
	L.SetTop(2)
	switch L.Get(1).(type) {
	case *LFunction, *LUserData, *LState:
		L.SetFEnv(L.Get(1), L.Get(2))
	default:
		L.RaiseError("'setfenv' cannot change environment of given object")
	}
	L.SetTop(1)
	return 1
}

func debugSetLocal(L *LState) int {
	// go-moonscript patch: an optional thread first, as in Lua 5.1
	L1, arg := L, 0
	if th, ok := L.Get(1).(*LState); ok {
		L1, arg = th, 1
	}
	level := L.CheckInt(arg + 1)
	idx := L.CheckInt(arg + 2)
	value := L.CheckAny(arg + 3)
	dbg, yield, ok := threadStack(L, L1, level)
	if !ok {
		L.ArgError(arg+1, "level out of range")
	}
	if yield != nil {
		L.Push(LNil)
		return 1
	}
	name := L1.SetLocal(dbg, idx, value)
	if len(name) > 0 {
		L.Push(LString(name))
	} else {
		L.Push(LNil)
	}
	return 1
}

// go-moonscript patch: debug.setmetatable is db_setmetatable: true.
func debugSetMetatable(L *LState) int {
	if t := L.Get(2).Type(); t != LTNil && t != LTTable {
		L.ArgError(2, "nil or table expected")
	}
	L.SetMetatable(L.Get(1), L.Get(2))
	L.Push(LTrue)
	return 1
}

func debugSetUpvalue(L *LState) int {
	fn := L.CheckFunction(1)
	idx := L.CheckInt(2)
	value := L.CheckAny(3)
	name := L.SetUpvalue(fn, idx, value)
	if len(name) > 0 {
		L.Push(LString(name))
	} else {
		L.Push(LNil)
	}
	return 1
}

// go-moonscript patch: debug.traceback is db_errorfb: an optional thread
// first (level 0 by default for another thread, whose level 0 is the yield
// a suspended coroutine is in), a message that is not a string or a
// number returned as is, and no "[G]: ?" line at the bottom of a
// coroutine.
func debugTraceback(L *LState) int {
	L1, arg := L, 0
	if th, ok := L.Get(1).(*LState); ok {
		L1, arg = th, 1
	}
	level := 1
	if L1 != L {
		level = 0
	}
	if n, ok := argNumber(L.Get(arg + 2)); ok {
		level = int(n)
	}
	prefix := ""
	if L.GetTop() > arg {
		switch v := L.Get(arg + 1).(type) {
		case LString, LNumber:
			prefix = LVAsString(v) + "\n"
		default:
			L.Push(v)
			return 1
		}
	}
	var traceback string
	switch {
	case L1 != L && L1 != L.G.CurrentThread && (L1.Dead || threadLevelOffset(L, L1) == 0):
		traceback = "stack traceback:"
	case L1 != L && L1 != L.G.CurrentThread:
		if level == 0 {
			traceback = strings.Replace(L1.stackTrace(0), "\n", "\n\t[G]: in function 'yield'\n", 1)
		} else {
			traceback = L1.stackTrace(level - 1)
		}
	default:
		traceback = L1.stackTrace(level)
	}
	traceback = strings.TrimSpace(traceback)
	if L1 != L1.G.MainThread {
		traceback = strings.TrimSuffix(traceback, "\n\t[G]: ?")
	}
	L.Push(LString(prefix + traceback))
	return 1
}

// IsTailCallLevel reports whether a stack level is one of the levels a tail
// call replaced (Lua 5.1 shows them as "(tail call)"), which GetStack
// answers with the bottom frame (go-moonscript patch).
func (ls *LState) IsTailCallLevel(level int) bool { return ls.isTailCallLevel(level) }

// threadLevelOffset is 1 for a suspended coroutine L1 (not the running
// thread), whose level 0 is the yield it is suspended in, 0 otherwise.
func threadLevelOffset(L, L1 *LState) int {
	if L1 == L || L1 == L.G.CurrentThread || L1.Dead {
		return 0
	}
	if L1.currentFrame == nil {
		if L1.yieldNRet == yieldedOut {
			return 1
		}
		return 0 // not started
	}
	if L1.stack.Sp() > 1 || L1.currentFrame.Pc > 0 {
		return 1 // it has yielded
	}
	return 0
}

// threadStack is lua_getstack(L1, level) (go-moonscript patch): the frame
// at level, or, at level 0 of a suspended coroutine, the yield function it
// is suspended in (gopher-lua keeps no frame for it); ok false for a level
// out of range, and for any level of a dead or not yet started coroutine.
func threadStack(L, L1 *LState, level int) (dbg *Debug, yield *LFunction, ok bool) {
	if L1 != L && L1 != L.G.CurrentThread {
		if L1.Dead || level < 0 {
			return nil, nil, false
		}
		if threadLevelOffset(L, L1) == 0 {
			return nil, nil, false // not started: no frame yet
		}
		if level == 0 {
			if co, ok := L.GetField(L.Get(RegistryIndex), "_LOADED").(*LTable); ok {
				if mod, ok := co.RawGetString(CoroutineLibName).(*LTable); ok {
					if fn, ok := mod.RawGetString("yield").(*LFunction); ok {
						return nil, fn, true
					}
				}
			}
			return nil, L.NewFunction(coYield), true
		}
		level--
	}
	dbg, ok = L1.GetStack(level)
	return dbg, nil, ok
}

// debugGetRegistry is debug.getregistry (go-moonscript patch).
func debugGetRegistry(L *LState) int {
	L.Push(L.Get(RegistryIndex))
	return 1
}

// debugDebug is db_debug (go-moonscript patch): the lines of the standard
// input (the io library's) run, each a chunk "=(debug command)", until
// "cont" or the end of the input; the errors go to the standard error.
func debugDebug(L *LState) int {
	in := L.Stdin()
	for {
		_, _ = os.Stderr.WriteString("lua_debug> ")
		line, ok := fgets(in, 250)
		if !ok || line == "cont\n" {
			return 0
		}
		fn, err := L.Load(strings.NewReader(line), "(debug command)") // a display name, "=(debug command)" in Lua 5.1
		if err == nil {
			L.Push(fn)
			err = L.PCall(0, 0, nil)
		}
		if err != nil {
			msg := err.Error()
			if ae, ok := err.(*ApiError); ok {
				msg = ae.Object.String()
			}
			_, _ = os.Stderr.WriteString(msg + "\n")
		}
		L.SetTop(0)
	}
}

// fgets reads a line (with its end) of at most size-1 bytes, false at the
// end of the input.
func fgets(r io.Reader, size int) (string, bool) {
	var b []byte
	var c [1]byte
	for len(b) < size-1 {
		n, err := r.Read(c[:])
		if n == 0 || err != nil {
			break
		}
		b = append(b, c[0])
		if c[0] == '\n' {
			break
		}
	}
	return string(b), len(b) > 0
}
