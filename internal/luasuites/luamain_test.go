//go:build linux

package luasuites

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"syscall"

	lua "github.com/tarantool-contrib/go-moonscript/lua"

	moonrt "github.com/tarantool-contrib/go-moonscript/moonscript/runtime"
)

// childEnv, set to the program name, makes the test binary act as a Lua
// interpreter (luaMain) instead of running the tests. The workspace's `lua`
// wrapper script sets it, so the suites can run it as a command, and run
// it again through io.popen and os.execute.
const childEnv = "LUASUITES_LUA"

// dialectEnv selects the dialect of the interpreter (runtime.ParseDialect:
// "luajit" for the LuaJIT views, the default lua51 otherwise); the
// interpreters a script starts inherit it.
const dialectEnv = "LUASUITES_DIALECT"

// Resource limits of an interpreter process: a runaway test fails instead
// of exhausting the machine.
const (
	childAddressSpace = 3 << 30
	childMaxStack     = 256 << 20
	// childMemoryLimit is the state's memory limit: an allocation past it
	// raises "not enough memory" (as lua5.1 does when malloc fails under
	// the same address space limit), which the scripts can catch, rather
	// than crash the Go runtime. A third of the address space: Go's heap
	// may hold up to twice the live data between collections, and the
	// runtime reserves address space beyond the heap.
	childMemoryLimit = childAddressSpace / 3
)

// luaMain is a minimal lua.c (Lua 5.1's standalone interpreter) over the
// VM of the moon runner: lua [-e stat] [-l mod] [-v] [--] [script [args]],
// or "-" for a script read from standard input. Errors are reported as
// "lua: <message>" and a traceback on standard error, with exit status 1.
// progname is arg's first entry. It returns the exit status.
func luaMain(progname string, args []string) int {
	_ = syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: childAddressSpace, Max: childAddressSpace})
	debug.SetMaxStack(childMaxStack)

	dialect, derr := moonrt.ParseDialect(os.Getenv(dialectEnv))
	if derr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "lua: %s: %v\n", dialectEnv, derr)
		return 1
	}
	L := moonrt.NewState(moonrt.StateOptions(dialect))
	defer L.Close()
	L.SetMemoryLimit(childMemoryLimit)

	// errors go to a copy of the standard error, which the script may close
	// (io.stderr:close() does on our VM)
	errOut := os.Stderr
	if fd, err := syscall.Dup(2); err == nil {
		errOut = os.NewFile(uintptr(fd), "stderr")
	}
	report := func(err error) int {
		_, _ = fmt.Fprintf(errOut, "lua: %v\n", err)
		return 1
	}
	// docall calls fn with the given arguments as lua.c does, with a
	// traceback for an error.
	docall := func(fn lua.LValue, args ...lua.LValue) error {
		L.Push(fn)
		for _, a := range args {
			L.Push(a)
		}
		tb := L.GetField(L.GetGlobal("debug"), "traceback")
		var handler *lua.LFunction
		if f, ok := tb.(*lua.LFunction); ok {
			handler = f
		}
		return L.PCall(len(args), 0, handler)
	}
	global := func(name string) lua.LValue { return L.GetGlobal(name) }
	load := func(fn string, args ...lua.LValue) (lua.LValue, error) {
		if err := L.CallByParam(lua.P{Fn: global(fn), NRet: 2, Protect: true}, args...); err != nil {
			return nil, err
		}
		chunk, msg := L.Get(-2), L.Get(-1)
		L.Pop(2)
		if chunk == lua.LNil {
			return nil, fmt.Errorf("%s", lua.LVAsString(msg))
		}
		return chunk, nil
	}

	// arg as lua.c sets it: the script at 0, the interpreter and its
	// options before it, the script's arguments after it.
	script := len(args)
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			script = i + 1
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			script = i
			break
		}
		if a == "-e" || a == "-l" {
			i++
		}
	}
	argt := L.NewTable()
	all := append([]string{progname}, args...)
	for i, a := range all {
		argt.RawSetInt(i-(script+1), lua.LString(a))
	}
	L.SetGlobal("arg", argt)

	for i := 0; i < script && i < len(args); i++ {
		switch a := args[i]; a {
		case "--":
		case "-v":
			fmt.Println(lua.LuaVersion + "  (go-moonscript test interpreter)")
		case "-e", "-l":
			if i+1 >= len(args) {
				return report(fmt.Errorf("'%s' needs argument", a))
			}
			i++
			var err error
			if a == "-e" {
				var chunk lua.LValue
				if chunk, err = load("loadstring", lua.LString(args[i]), lua.LString("=(command line)")); err == nil {
					err = docall(chunk)
				}
			} else {
				err = docall(global("require"), lua.LString(args[i]))
			}
			if err != nil {
				return report(err)
			}
		default:
			return report(fmt.Errorf("unrecognized option '%s'", a))
		}
	}
	if script >= len(args) {
		return 0
	}
	var (
		chunk lua.LValue
		err   error
	)
	if args[script] == "-" {
		var src []byte
		if src, err = io.ReadAll(os.Stdin); err == nil {
			chunk, err = load("loadstring", lua.LString(src), lua.LString("=stdin"))
		}
	} else {
		chunk, err = load("loadfile", lua.LString(args[script]))
	}
	if err != nil {
		return report(err)
	}
	var sargs []lua.LValue
	for _, a := range args[script+1:] {
		sargs = append(sargs, lua.LString(a))
	}
	if err := docall(chunk, sargs...); err != nil {
		return report(err)
	}
	return 0
}
