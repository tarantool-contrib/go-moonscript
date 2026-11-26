package lua

// go-moonscript patch: the Lua 5.1 library functions that gopher-lua
// lacked: table.foreach, table.foreachi and table.setn (lbaselib.c,
// ltablib.c with LUA_COMPAT_GETN off; gcinfo is in gc.go), and package.loadlib's results when
// no C library can be loaded (loadlib.c). LuaJIT 2.1 has no table.setn.

import (
	"os"
)

// tableForeachi calls f(i, t[i]) for i = 1..#t until f returns a non-nil
// value, which it returns.
func tableForeachi(L *LState) int {
	tbl := L.CheckTable(1)
	n := tbl.Len()
	fn := L.CheckFunction(2)
	for i := 1; i <= n; i++ {
		L.Push(fn)
		L.Push(LNumber(i))
		L.Push(tbl.RawGetInt(i))
		L.Call(2, 1)
		if L.Get(-1) != LNil {
			return 1
		}
		L.Pop(1)
	}
	return 0
}

// tableForeach calls f(k, v) for the pairs of t, in next's order, until f
// returns a non-nil value, which it returns.
func tableForeach(L *LState) int {
	tbl := L.CheckTable(1)
	fn := L.CheckFunction(2)
	k, v := tbl.Next(LNil)
	for k != LNil {
		L.Push(fn)
		L.Push(k)
		L.Push(v)
		L.Call(2, 1)
		if L.Get(-1) != LNil {
			return 1
		}
		L.Pop(1)
		k, v = tbl.Next(k)
	}
	return 0
}

// tableSetN is table.setn of Lua 5.1, built without LUA_COMPAT_GETN.
func tableSetN(L *LState) int {
	L.CheckTable(1)
	L.RaiseError("'setn' is obsolete")
	return 0
}

// loLoadLib is package.loadlib on a system without dynamic libraries for
// Lua: a library that dlopen would not find gives dlopen's error ("open"),
// any other the message of a Lua 5.1 built without them ("absent"), as no C
// function can be called.
func loLoadLib(L *LState) int {
	path := L.CheckString(1)
	L.CheckString(2)
	L.Push(LNil)
	if _, err := os.Stat(path); err != nil {
		L.Push(LString(path + ": cannot open shared object file: No such file or directory"))
		L.Push(LString("open"))
		return 3
	}
	L.Push(LString("dynamic libraries not enabled; check your Lua installation"))
	L.Push(LString("absent"))
	return 3
}
