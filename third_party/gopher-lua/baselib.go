package lua

import (
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"strings"
)

/* basic functions {{{ */

func OpenBase(L *LState) int {
	global := L.Get(GlobalsIndex).(*LTable)
	L.SetGlobal("_G", global)
	L.SetGlobal("_VERSION", LString(LuaVersion))
	L.SetGlobal("_GOPHER_LUA_VERSION", LString(PackageName+" "+PackageVersion))
	basemod := L.RegisterModule("_G", baseFuncs)
	global.RawSetString("ipairs", L.NewClosure(baseIpairs, L.NewFunction(ipairsaux)))
	global.RawSetString("pairs", L.NewClosure(basePairs, L.NewFunction(pairsaux)))
	openLuaJIT(L, BaseLibName, basemod) // go-moonscript patch (luajitlib.go)
	L.Push(basemod)
	return 1
}

var baseFuncs = map[string]LGFunction{
	"assert":         baseAssert,
	"collectgarbage": baseCollectGarbage, // go-moonscript patch (gc.go)
	"dofile":         baseDoFile,
	"error":          baseError,
	"gcinfo":         baseGcInfo, // go-moonscript patch (gc.go)
	"getfenv":        baseGetFEnv,
	"getmetatable":   baseGetMetatable,
	"load":           baseLoad,
	"loadfile":       baseLoadFile,
	"loadstring":     baseLoadString,
	"next":           baseNext,
	"pcall":          basePCall,
	"print":          basePrint,
	"rawequal":       baseRawEqual,
	"rawget":         baseRawGet,
	"rawset":         baseRawSet,
	"select":         baseSelect,
	"_printregs":     base_PrintRegs,
	"setfenv":        baseSetFEnv,
	"setmetatable":   baseSetMetatable,
	"tonumber":       baseToNumber,
	"tostring":       baseToString,
	"type":           baseType,
	"unpack":         baseUnpack,
	"xpcall":         baseXPCall,
	// loadlib
	"module":  loModule,
	"require": loRequire,
	// hidden features
	"newproxy": baseNewProxy, // go-moonscript patch (gc.go)
}

func baseAssert(L *LState) int {
	if L.Dialect() == DialectLuaJIT { // go-moonscript patch (luajitlib.go)
		return jitAssert(L)
	}
	L.CheckAny(1) // go-moonscript patch: as Lua 5.1
	if !L.ToBool(1) {
		L.RaiseError("%s", L.OptString(2, "assertion failed!"))
		return 0
	}
	return L.GetTop()
}

func baseDoFile(L *LState) int {
	src := L.ToString(1)
	top := L.GetTop()
	fn, err := L.LoadFile(src)
	if err != nil {
		L.Push(LString(err.Error()))
		L.Panic(L)
	}
	L.Push(fn)
	L.Call(0, MultRet)
	return L.GetTop() - top
}

func baseError(L *LState) int {
	// go-moonscript patch: Lua 5.1's luaB_error: any value, none is nil; a
	// number gets the position too (and becomes a string)
	level := L.OptInt(2, 1)
	obj := L.Get(1)
	if n, ok := obj.(LNumber); ok && level > 0 {
		obj = LString(n.String())
	}
	L.Error(obj, level)
	return 0
}

func baseGetFEnv(L *LState) int {
	fn := getFEnvFunc(L, true)
	if fn.IsG {
		L.Push(L.Env) // the thread's global environment
	} else {
		L.Push(fn.Env)
	}
	return 1
}

// getFEnvFunc is lbaselib.c getfunc (go-moonscript patch): the function
// argument 1 is, or the one running at level argument 1 (optional if opt,
// 1 by default), with Lua 5.1's errors for levels.
func getFEnvFunc(L *LState, opt bool) *LFunction {
	if fn, ok := L.Get(1).(*LFunction); ok {
		return fn
	}
	var level int
	if opt {
		level = int(cInt(float64(L.OptNumber(1, 1))))
	} else {
		level = int(cInt(float64(L.CheckNumber(1))))
	}
	if level < 0 {
		L.ArgError(1, "level must be non-negative")
	}
	dbg, ok := L.GetStack(level)
	if !ok {
		L.ArgError(1, "invalid level")
	}
	if L.isTailCallLevel(level) {
		L.RaiseError("no function environment for tail call at level %d", level)
	}
	return dbg.frame.Fn
}

func baseGetMetatable(L *LState) int {
	L.Push(L.GetMetatable(L.CheckAny(1)))
	return 1
}

func ipairsaux(L *LState) int {
	tb := L.CheckTable(1)
	i := L.CheckInt(2)
	i++
	v := tb.RawGetInt(i)
	if v == LNil {
		return 0
	} else {
		L.Pop(1)
		L.Push(LNumber(i))
		L.Push(LNumber(i))
		L.Push(v)
		return 2
	}
}

func baseIpairs(L *LState) int {
	tb := L.CheckTable(1)
	L.Push(L.Get(UpvalueIndex(1)))
	L.Push(tb)
	L.Push(LNumber(0))
	return 3
}

func loadaux(L *LState, reader io.Reader, chunkname string) int {
	if fn, err := L.Load(reader, chunkname); err != nil {
		L.Push(LNil)
		L.Push(LString(err.Error()))
		return 2
	} else {
		L.Push(fn)
		return 1
	}
}

// go-moonscript patch: load is Lua 5.1's load(func [, chunkname]): the
// pieces the reader function returns are loaded as one chunk (an error in
// the reader is returned as the load's); LuaJIT's load(chunk [, chunkname
// [, mode [, env]]]), which takes a string too, is jitLoad (luajitlib.go).
// The original took a string, a mode and an env as LuaJIT (from
// awirix/lua bd6991bfbf).
func baseLoad(L *LState) int {
	if L.Dialect() == DialectLuaJIT {
		return jitLoad(L)
	}
	chunkname := L.OptString(2, "?")
	fn := L.CheckFunction(1)
	top := L.GetTop()
	var buf strings.Builder
	for {
		L.SetTop(top)
		L.Push(fn)
		if err := L.PCall(0, 1, nil); err != nil {
			L.SetTop(top)
			L.Push(LNil)
			if aerr, ok := err.(*ApiError); ok {
				L.Push(aerr.Object)
			} else {
				L.Push(LString(err.Error()))
			}
			return 2
		}
		ret := L.Get(-1)
		if ret == LNil {
			break
		} else if LVCanConvToString(ret) {
			str := ret.String()
			if len(str) == 0 {
				break
			}
			buf.WriteString(str)
		} else {
			L.SetTop(top)
			L.Push(LNil)
			L.Push(LString("reader function must return a string"))
			return 2
		}
	}
	L.SetTop(top)
	return loadaux(L, strings.NewReader(buf.String()), chunkname)
}

func baseLoadFile(L *LState) int {
	if L.Dialect() == DialectLuaJIT { // go-moonscript patch (luajitlib.go)
		return jitLoadFile(L)
	}
	var reader io.Reader
	var chunkname string
	var err error
	if L.GetTop() < 1 {
		reader = L.Stdin() // go-moonscript patch: the io library's stdin
		chunkname = "<stdin>"
	} else {
		chunkname = L.CheckString(1)
		reader, err = os.Open(chunkname)
		if err != nil {
			L.Push(LNil)
			L.Push(LString(fmt.Sprintf("can not open file: %v", chunkname)))
			return 2
		}
		defer reader.(*os.File).Close()
	}
	return loadaux(L, reader, chunkname)
}

func baseLoadString(L *LState) int {
	if L.Dialect() == DialectLuaJIT { // go-moonscript patch: load's alias
		return jitLoad(L)
	}
	chunk := L.CheckString(1)
	name := "<string>"
	if IsBinaryChunk(chunk) {
		name = chunk // go-moonscript patch: as in Lua 5.1, for its errors (dump.go)
	}
	return loadaux(L, strings.NewReader(chunk), L.OptString(2, name))
}

func baseNext(L *LState) int {
	tb := L.CheckTable(1)
	index := LNil
	if L.GetTop() >= 2 {
		index = L.Get(2)
	}
	key, value, ok := tb.next(index)
	if !ok {
		L.raiseError(0, "invalid key to 'next'") // go-moonscript patch
	}
	if key == LNil {
		L.Push(LNil)
		return 1
	}
	L.Push(key)
	L.Push(value)
	return 2
}

func pairsaux(L *LState) int {
	tb := L.CheckTable(1)
	key, value, ok := tb.next(L.Get(2))
	if !ok {
		L.raiseError(0, "invalid key to 'next'") // go-moonscript patch
	}
	if key == LNil {
		return 0
	} else {
		L.Pop(1)
		L.Push(key)
		L.Push(key)
		L.Push(value)
		return 2
	}
}

func basePairs(L *LState) int {
	tb := L.CheckTable(1)
	L.Push(L.Get(UpvalueIndex(1)))
	L.Push(tb)
	L.Push(LNil)
	return 3
}

func basePCall(L *LState) int {
	L.CheckAny(1)
	v := L.Get(1)
	if v.Type() != LTFunction && L.GetMetaField(v, "__call").Type() != LTFunction {
		L.Push(LFalse)
		L.Push(LString("attempt to call a " + v.Type().String() + " value"))
		return 2
	}
	nargs := L.GetTop() - 1
	if err := L.PCall(nargs, MultRet, nil); err != nil {
		L.Push(LFalse)
		if aerr, ok := err.(*ApiError); ok {
			L.Push(aerr.Object)
		} else {
			L.Push(LString(err.Error()))
		}
		return 2
	} else {
		L.Insert(LTrue, 1)
		return L.GetTop()
	}
}

// go-moonscript patch: print writes to the buffered standard output of the
// io library (LState.Stdout), as Lua 5.1's writes to C's stdout, and calls
// the global tostring, which must return a string (or a number).
func basePrint(L *LState) int {
	top := L.GetTop()
	tostring := L.GetGlobal("tostring")
	var sb strings.Builder
	for i := 1; i <= top; i++ {
		L.Push(tostring)
		L.Push(L.Get(i))
		L.Call(1, 1)
		switch v := L.Get(-1).(type) {
		case LString:
			sb.WriteString(string(v))
		case LNumber:
			sb.WriteString(v.String())
		default:
			L.RaiseError("'tostring' must return a string to 'print'")
		}
		L.Pop(1)
		if i != top {
			sb.WriteByte('\t')
		}
	}
	sb.WriteByte('\n')
	_, _ = io.WriteString(L.Stdout(), sb.String())
	return 0
}

func base_PrintRegs(L *LState) int {
	L.printReg()
	return 0
}

func baseRawEqual(L *LState) int {
	if L.CheckAny(1) == L.CheckAny(2) {
		L.Push(LTrue)
	} else {
		L.Push(LFalse)
	}
	return 1
}

func baseRawGet(L *LState) int {
	L.Push(L.RawGet(L.CheckTable(1), L.CheckAny(2)))
	return 1
}

func baseRawSet(L *LState) int {
	tb, key, value := L.CheckTable(1), L.CheckAny(2), L.CheckAny(3)
	// go-moonscript patch: Lua 5.1 raises these errors without a position
	// (yuin/gopher-lua#319), and rawset returns the table.
	if key == LNil {
		L.raiseError(0, "table index is nil")
	} else if n, ok := key.(LNumber); ok && n != n {
		L.raiseError(0, "table index is NaN")
	}
	tb.RawSet(key, value)
	L.SetTop(1)
	return 1
}

// go-moonscript patch: as Lua 5.1's select, a string starting with '#'
// asks for the count, any other argument is an integer (a string that
// converts to a number too).
func baseSelect(L *LState) int {
	if lv, ok := L.Get(1).(LString); ok && strings.HasPrefix(string(lv), "#") {
		L.Push(LNumber(L.GetTop() - 1))
		return 1
	}
	idx := L.CheckInt(1)
	num := L.GetTop()
	if idx < 0 {
		idx = num + idx
	} else if idx > num {
		idx = num
	}
	if 1 > idx {
		L.ArgError(1, "index out of range")
	}
	return num - idx
}

func baseSetFEnv(L *LState) int {
	env := L.CheckTable(2)
	fn := getFEnvFunc(L, false)
	if n, ok := L.Get(1).(LNumber); ok && n == 0 {
		L.Env = env // the environment of the running thread
		return 0
	}
	if fn.IsG {
		L.RaiseError("'setfenv' cannot change environment of given object")
	}
	fn.Env = env
	L.Push(fn)
	return 1
}

func baseSetMetatable(L *LState) int {
	// go-moonscript patch: Lua 5.1's checks, a table first
	L.CheckTable(1)
	if t := L.Get(2).Type(); t != LTNil && t != LTTable {
		L.ArgError(2, "nil or table expected")
	}
	obj := L.Get(1)
	mt := L.Get(2)
	if m := L.metatable(obj, true); m != LNil {
		if tb, ok := m.(*LTable); ok && tb.RawGetString("__metatable") != LNil {
			L.RaiseError("cannot change a protected metatable")
		}
	}
	L.SetMetatable(obj, mt)
	L.SetTop(1)
	return 1
}

// go-moonscript patch: tonumber with a base other than 10 is Lua 5.1's:
// C's strtoul over the string (a number is converted to one: white space,
// a sign, "0x" for base 16, saturation at ULONG_MAX), then nothing but white
// space; a base out of 2..36 is an error. A '-' negates the value, as in
// LuaJIT 2.1, Lua 5.2+ and the Lua 5.1 test suite (tonumber("-10", 36) ==
// -36), where PUC Lua 5.1 gives the unsigned long that strtoul wraps it to
// (2^64 - 36 on 64-bit systems).
func baseToNumber(L *LState) int {
	base := L.OptInt(2, 10)
	if base == 10 {
		switch lv := L.CheckAny(1).(type) {
		case LNumber:
			L.Push(L.Get(1)) // go-moonscript patch: without boxing it again
		case LString:
			if L.Dialect() == DialectLuaJIT && strings.IndexByte(string(lv), 0) >= 0 {
				L.Push(LNil) // go-moonscript patch: LuaJIT scans the whole string
				break
			}
			// go-moonscript patch (halogencapital/gopher-lua d3de08565a,
			// yuin/gopher-lua#478): as Lua 5.1, a string converts like in
			// arithmetic ("1e-06" was nil)
			if v, err := parseNumber(string(lv)); err != nil {
				L.Push(LNil)
			} else {
				pushNumber(L, v)
			}
		default:
			L.Push(LNil)
		}
		return 1
	}
	str := L.CheckString(1)
	if base < 2 || base > 36 {
		L.ArgError(2, "base out of range")
	}
	if i := strings.IndexByte(str, 0); i >= 0 {
		str = str[:i] // the C string ends at the first NUL
	}
	if v, neg, n := strtoul(str, base); n > 0 && strings.TrimLeft(str[n:], cSpaces) == "" {
		switch {
		case !neg:
			L.Push(LNumber(v))
		case L.Dialect() == DialectLuaJIT:
			// go-moonscript patch: LuaJIT 2.1 negates the value
			L.Push(LNumber(-float64(v)))
		case v == math.MaxUint64 && strtoulOverflows(str, base):
			L.Push(LNumber(v)) // ULONG_MAX, whatever the sign
		default:
			L.Push(LNumber(-v)) // Lua 5.1: strtoul's negation modulo 2^64
		}
	} else {
		L.Push(LNil)
	}
	return 1
}

// strtoul is C's strtoul for a base from 2 to 36: the value of the longest
// prefix of s that is a number (after white space, an optional sign and,
// for base 16, "0x"; ULONG_MAX if it overflows), whether the sign is '-',
// and the length of that prefix (0: none).
func strtoul(s string, base int) (uint64, bool, int) {
	i := len(s) - len(strings.TrimLeft(s, cSpaces))
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	if base == 16 && i+2 < len(s) && s[i] == '0' && s[i+1]|0x20 == 'x' && strtoulDigit(s[i+2]) < 16 {
		i += 2
	}
	start := i
	var n uint64
	overflow := false
	for ; i < len(s) && strtoulDigit(s[i]) < base; i++ {
		hi, lo := bits.Mul64(n, uint64(base))
		lo, carry := bits.Add64(lo, uint64(strtoulDigit(s[i])), 0)
		overflow = overflow || hi != 0 || carry != 0
		n = lo
	}
	switch {
	case i == start:
		return 0, false, 0
	case overflow:
		return math.MaxUint64, neg, i
	}
	return n, neg, i
}

// strtoulOverflows reports whether the number strtoul reads from s
// overflows an unsigned long.
func strtoulOverflows(s string, base int) bool {
	i := len(s) - len(strings.TrimLeft(s, cSpaces))
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	if base == 16 && i+2 < len(s) && s[i] == '0' && s[i+1]|0x20 == 'x' && strtoulDigit(s[i+2]) < 16 {
		i += 2
	}
	var n uint64
	for ; i < len(s) && strtoulDigit(s[i]) < base; i++ {
		hi, lo := bits.Mul64(n, uint64(base))
		lo, carry := bits.Add64(lo, uint64(strtoulDigit(s[i])), 0)
		if hi != 0 || carry != 0 {
			return true
		}
		n = lo
	}
	return false
}

// strtoulDigit is the value of a digit for strtoul, 36 for none.
func strtoulDigit(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'a' <= c|0x20 && c|0x20 <= 'z':
		return int(c|0x20-'a') + 10
	}
	return 36
}

func baseToString(L *LState) int {
	v1 := L.CheckAny(1)
	// go-moonscript patch: luaL_callmeta: a __tostring that is there is
	// called (any callable; an error for other values), its result returned
	if mm := L.metaOp1(v1, "__tostring"); mm != LNil {
		L.SetTop(1)
		L.Push(mm)
		L.Push(v1)
		L.Call(1, 1)
		return 1
	}
	L.Push(L.ToStringMeta(v1))
	return 1
}

func baseType(L *LState) int {
	L.Push(LString(L.CheckAny(1).Type().String()))
	return 1
}

func baseUnpack(L *LState) int {
	tb := L.CheckTable(1)
	start := L.OptInt(2, 1)
	end := L.OptInt(3, tb.Len())
	for i := start; i <= end; i++ {
		L.Push(tb.RawGetInt(i))
	}
	ret := end - start + 1
	if ret < 0 {
		return 0
	}
	return ret
}

// go-moonscript patch: xpcall is Lua 5.1's: the function and the handler
// may be any values (a callable table is called; a handler that is not a
// function makes an error "error in error handling"), the arguments after
// the handler are ignored. Under DialectLuaJIT it is LuaJIT 2.1's: the
// handler must be a function and the arguments after it are passed to the
// function.
func baseXPCall(L *LState) int {
	if L.Dialect() == DialectLuaJIT {
		L.CheckFunction(2)
	} else {
		L.CheckAny(2)
		L.SetTop(2)
	}
	errfunc, _ := L.Get(2).(*LFunction)
	L.Remove(2)
	v := L.Get(1)
	if v.Type() != LTFunction && L.GetMetaField(v, "__call").Type() != LTFunction {
		L.SetTop(0)
		if errfunc == nil {
			L.Push(LFalse)
			L.Push(LString("error in error handling"))
			return 2
		}
		L.Push(errfunc)
		L.Push(LString("attempt to call a " + v.Type().String() + " value"))
		if err := L.PCall(1, 1, nil); err != nil {
			L.SetTop(0)
			L.Push(LFalse)
			L.Push(LString("error in error handling"))
			return 2
		}
		L.Insert(LFalse, 1)
		return 2
	}
	nargs := L.GetTop() - 1
	if err := L.PCall(nargs, MultRet, errfunc); err != nil {
		L.Push(LFalse)
		if errfunc == nil {
			L.Push(LString("error in error handling"))
		} else if aerr, ok := err.(*ApiError); ok {
			L.Push(aerr.Object)
		} else {
			L.Push(LString(err.Error()))
		}
		return 2
	}
	L.Insert(LTrue, 1)
	return L.GetTop()
}

/* }}} */

/* load lib {{{ */

func loModule(L *LState) int {
	name := L.CheckString(1)
	loaded := L.GetField(L.Get(RegistryIndex), "_LOADED")
	tb := L.GetField(loaded, name)
	if _, ok := tb.(*LTable); !ok {
		tb = L.FindTable(L.Get(GlobalsIndex).(*LTable), name, 1)
		if tb == LNil {
			L.RaiseError("name conflict for module: %v", name)
		}
		L.SetField(loaded, name, tb)
	}
	if L.GetField(tb, "_NAME") == LNil {
		L.SetField(tb, "_M", tb)
		L.SetField(tb, "_NAME", LString(name))
		names := strings.Split(name, ".")
		pname := ""
		if len(names) > 1 {
			pname = strings.Join(names[:len(names)-1], ".") + "."
		}
		L.SetField(tb, "_PACKAGE", LString(pname))
	}

	caller := L.currentFrame.Parent
	if caller == nil {
		L.RaiseError("no calling stack.")
	} else if caller.Fn.IsG {
		L.RaiseError("module() can not be called from GFunctions.")
	}
	L.SetFEnv(caller.Fn, tb)

	top := L.GetTop()
	for i := 2; i <= top; i++ {
		L.Push(L.Get(i))
		L.Push(tb)
		L.Call(1, 0)
	}
	L.Push(tb)
	return 1
}

var loopdetection = &LUserData{}

func loRequire(L *LState) int {
	name := L.CheckString(1)
	loaded := L.GetField(L.Get(RegistryIndex), "_LOADED")
	lv := L.GetField(loaded, name)
	if LVAsBool(lv) {
		if lv == loopdetection {
			L.RaiseError("loop or previous error loading module: %s", name)
		}
		L.Push(lv)
		return 1
	}
	loaders, ok := L.GetField(L.Get(RegistryIndex), "_LOADERS").(*LTable)
	if !ok {
		L.RaiseError("package.loaders must be a table")
	}
	messages := []string{}
	var modasfunc LValue
	for i := 1; ; i++ {
		loader := L.RawGetInt(loaders, i)
		if loader == LNil {
			L.RaiseError("module %s not found:\n\t%s, ", name, strings.Join(messages, "\n\t"))
		}
		L.Push(loader)
		L.Push(LString(name))
		L.Call(1, 1)
		ret := L.reg.Pop()
		switch retv := ret.(type) {
		case *LFunction:
			modasfunc = retv
			goto loopbreak
		case LString:
			messages = append(messages, string(retv))
		}
	}
loopbreak:
	L.SetField(loaded, name, loopdetection)
	L.Push(modasfunc)
	L.Push(LString(name))
	L.Call(1, 1)
	ret := L.reg.Pop()
	modv := L.GetField(loaded, name)
	if ret != LNil && modv == loopdetection {
		L.SetField(loaded, name, ret)
		L.Push(ret)
	} else if modv == loopdetection {
		L.SetField(loaded, name, LTrue)
		L.Push(LTrue)
	} else {
		L.Push(modv)
	}
	return 1
}

/* }}} */

/* hidden features {{{ */

/* }}} */

//
