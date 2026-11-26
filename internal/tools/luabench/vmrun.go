package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"syscall"

	moonrt "github.com/tarantool-contrib/go-moonscript/runtime"
	lua "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua"
)

// vmMain is the "vm" subcommand: the runner of our VM,
//
//	luabench vm [-cpuprofile file] wrap.lua <script> [args...]
//
// It sets up the Lua state the way cmd/moon does (moonrt.NewState: the
// standard libraries, the Lua 5.1 compatibility layer runtime.OpenCompat,
// the MoonScript modules in package.preload, Lua 5.1's call depth limit),
// sets the global arg as lua.c does for "lua wrap.lua <script> args..."
// and runs wrap.lua, whose clock is the process CPU time (getrusage:
// user+system time of all threads, so the Go runtime's concurrent GC
// workers are counted too).
func vmMain(argv []string) int {
	fs := flag.NewFlagSet("vm", flag.ContinueOnError)
	cpuprofile := fs.String("cpuprofile", "", "write a CPU profile of the run to `file`")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: luabench vm [-cpuprofile file] wrap.lua <script> [args...]")
		return 2
	}
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "luabench vm:", err)
			return 1
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintln(os.Stderr, "luabench vm:", err)
			return 1
		}
		defer func() {
			pprof.StopCPUProfile()
			_ = f.Close()
		}()
	}
	if err := runVM(fs.Arg(0), fs.Args()[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "luabench vm:", err)
		return 1
	}
	return 0
}

// runVM runs wrapper with args (the script and its arguments) on a fresh
// state; stderr receives Lua error messages with their traceback.
func runVM(wrapper string, args []string, stderr io.Writer) error {
	L := moonrt.NewState()
	defer L.Close()
	argt := L.NewTable()
	argt.RawSetInt(0, lua.LString(wrapper))
	for i, a := range args {
		argt.RawSetInt(i+1, lua.LString(a))
	}
	L.SetGlobal("arg", argt)
	L.SetGlobal("LUABENCH_CLOCK", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNumber(cpuSeconds()))
		return 1
	}))
	fn, err := L.LoadFile(wrapper)
	if err != nil {
		return err
	}
	handler, err := moonrt.LoadLua(L, `return debug.traceback(tostring((...)), 2)`, "=traceback")
	if err != nil {
		return err
	}
	L.Push(fn)
	if err := L.PCall(0, 0, handler); err != nil {
		var ae *lua.ApiError
		if errors.As(err, &ae) {
			_, _ = fmt.Fprintln(stderr, lua.LVAsString(ae.Object))
			return errors.New("script error")
		}
		return err
	}
	return nil
}

// cpuSeconds is the process CPU time (user + system) in seconds.
func cpuSeconds() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return tv(ru.Utime) + tv(ru.Stime)
}

func tv(t syscall.Timeval) float64 {
	return float64(t.Sec) + float64(t.Usec)/1e6
}
