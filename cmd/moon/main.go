// Command moon runs MoonScript files (Go port of moon v0.8.0 on gopher-lua).
//
//	moon [-h] [--coverage] [-d] [--execute <execute>] [--version] [<script>] [<args>] ...
//
// It runs upstream bin/moon.moon (cmd/moon/moon.moon, compiled into
// moon.lua by `go generate ./runtime`) on a Lua state set up by
// go-moonscript/runtime, so script arguments (the global arg: arg[0] the
// script, arg[1..] its arguments, arg[-1] the moon program), -e/--execute,
// -d, -v/--version, -h/--help, the .moon module loader and the rewritten
// error tracebacks ("moon: file.moon:12: (34) message") behave like the
// reference; argument parsing is an emulation of the argparse parser
// moon.moon builds (args.go), with the same usage and error texts and exit
// codes. --coverage/-c runs moonscript.cmd.coverage on the debug.sethook of
// the patched gopher-lua (third_party/gopher-lua/hook.go).
// Line events fall on the lines they do in PUC Lua (the patched compiler
// gives instructions luac's lines), so a hook of a script's own sees the
// same lines.
//
// Differences from the reference moon (PUC Lua 5.1):
//
//   - moon -c prints a coverage report. Upstream's never does on Lua 5.1:
//     its hook is a bound method, so a tail call, and debug.getinfo(2) in
//     it reports the "(tail call)" level, which has no line table. The
//     embedded moonscript.cmd.coverage is fixed on purpose (a hook that
//     isn't a tail call; files listed sorted; sources that aren't files
//     skipped), and the tests check it against the reference moon running
//     the same fixed module (scripts/gen-moon-runner-ref.sh).
//   - Errors that escape the driver (-d) are reported as "moon: <message>"
//     plus a traceback, where lua5.1 prints "lua5.1: <message>"; the frames
//     of the driver itself are named "moon:<line>".
//   - Tracebacks are gopher-lua's, relabeled like Lua 5.1's ("[C]", "(tail
//     call)", "main chunk"); long ones are cut to the first and last 7
//     frames (Lua 5.1: first 12 and last 10).
//   - Library modules are embedded: their errors name them
//     "moonscript/base.lua:91:" rather than by their installation path.
//   - No C modules (package.cpath is empty, no lpeg global), a different
//     pairs order, and Lua syntax errors in compiled code (only for
//     LuaJIT/5.2 syntax) are worded by gopher-lua's parser.
package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime/debug"

	lua "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua"

	moonrt "github.com/tarantool-contrib/go-moonscript/runtime"
)

//go:embed moon.lua
var driver string

// progname prefixes errors that escape the driver (-d mode), as lua.c's
// progname does.
const progname = "moon"

// defaultMemoryLimit is the state's memory limit when GOMEMLIMIT is not
// set: past it, building a string or growing a table raises "not enough
// memory", which a script can catch, as lua5.1 does when malloc fails,
// instead of a fatal error of the Go runtime (or the OOM killer) for an
// allocation explosion such as a string doubled in a loop. 4 GiB is far
// beyond what MoonScript programs use, and below what a 64-bit Lua 5.1 can
// allocate on most machines that run them.
const defaultMemoryLimit = 4 << 30

// memoryLimit is GOMEMLIMIT, the Go runtime's soft memory limit, if it is
// set, otherwise defaultMemoryLimit.
func memoryLimit() int64 {
	if limit := debug.SetMemoryLimit(-1); limit != math.MaxInt64 {
		return limit
	}
	return defaultMemoryLimit
}

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run runs moon with argv (argv[0] is the program). Scripts calling
// os.exit end the process directly.
func run(argv []string, stdout, stderr io.Writer) int {
	L := moonrt.NewState()
	defer L.Close()
	L.SetMemoryLimit(memoryLimit())

	// the global arg as lua.c sets it for a script: arg[0] is the script
	// (here the moon program), arg[1..] its arguments
	argt := L.NewTable()
	for i, a := range argv {
		argt.RawSetInt(i, lua.LString(a))
	}
	L.SetGlobal("arg", argt)

	parse := L.NewFunction(func(L *lua.LState) int {
		t := L.CheckTable(1)
		var args []string
		for i := 1; i <= t.Len(); i++ {
			args = append(args, lua.LVAsString(t.RawGetInt(i)))
		}
		o := parseArgs(args, stdout, stderr)
		if o.exitCode >= 0 {
			L.Close()
			os.Exit(o.exitCode)
		}
		res := L.NewTable()
		if o.script != nil {
			res.RawSetString("script", lua.LString(*o.script))
		}
		if o.execute != nil {
			res.RawSetString("execute", lua.LString(*o.execute))
		}
		rest := L.NewTable()
		for _, a := range o.args {
			rest.Append(lua.LString(a))
		}
		res.RawSetString("args", rest)
		for name, v := range o.flags {
			res.RawSetString(name, lua.LBool(v))
		}
		L.Push(res)
		return 1
	})

	fn, err := moonrt.LoadLua(L, driver, "@moon")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", progname, err)
		return 1
	}
	// lua.c's docall: a traceback message handler
	handler, err := moonrt.LoadLua(L, `local msg = ...
if type(msg) ~= "string" and type(msg) ~= "number" then return msg end
return debug.traceback(tostring(msg), 2)`, "=traceback")
	if err != nil {
		panic(err)
	}
	L.Push(fn)
	L.Push(parse)
	if err := L.PCall(1, 0, handler); err != nil {
		msg := "(error object is not a string)"
		var ae *lua.ApiError
		if errors.As(err, &ae) {
			if s, ok := ae.Object.(lua.LString); ok {
				msg = string(s)
			}
		} else {
			msg = err.Error()
		}
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", progname, msg)
		return 1
	}
	return 0
}
