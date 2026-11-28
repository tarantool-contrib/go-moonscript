package integration_test

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/luatest"
)

// The tests in this file are about what a bundle does when it runs: each one
// bundles a project with the CLI and compares the bundle with the original
// program on every interpreter.

// runOptions are the default run options.
func runOptions() luatest.RunOptions {
	return luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}
}

// cliCase is a differential test of project, bundled by the CLI with
// `--entry main.lua` and args.
func cliCase(project luatest.Project, args ...string) luatest.Case {
	return luatest.Case{
		Project: project,
		Dir:     "",
		Entry:   "",
		Bundle:  luatest.CLI(append([]string{"--entry", "main.lua"}, args...)...),
		Args:    nil,
		Options: runOptions(),
		Check:   nil,
	}
}

// bundleProject writes project into a fresh directory and bundles it with the
// CLI and args.
func bundleProject(tb testing.TB, project luatest.Project, args ...string) []byte {
	tb.Helper()

	return luatest.CLI(args...)(tb, luatest.WriteProject(tb, project))
}

// hostRuns runs a host script that gets the program to run as arg[1]: once
// next to the original project, with main.lua as the program, and once in a
// directory that holds nothing but the host and the bundle, with bundle.lua.
func hostRuns(
	t *testing.T, interp luatest.Interpreter, host string, project luatest.Project, bundle []byte,
) (luatest.Result, luatest.Result) {
	t.Helper()

	origProject := maps.Clone(project)
	origProject["host.lua"] = host
	origDir := luatest.WriteProject(t, origProject)
	bundleDir := luatest.WriteProject(t, luatest.Project{"host.lua": host, "bundle.lua": string(bundle)})

	return luatest.Run(t, interp, origDir, runOptions(), "host.lua", "main.lua"),
		luatest.Run(t, interp, bundleDir, runOptions(), "host.lua", "bundle.lua")
}

// showResults is a host that prints how many values the program returned to
// dofile, and the values.
const showResults = "local function show(...) print(select(\"#\", ...), ...) end\n" +
	"show(dofile(arg[1]))\n"

// TestBundle_EntrySeesCommandLineArguments: the entry's `...` holds the
// command-line arguments, as in a script, not the arguments require passes
// to a loader.
func TestBundle_EntrySeesCommandLineArguments(t *testing.T) {
	t.Parallel()

	spec := cliCase(luatest.Project{
		"main.lua": `print("n=" .. select("#", ...), ...)` + "\n",
	})
	spec.Args = []string{"x", "y"}

	luatest.Differential(t, spec)
}

// TestBundle_EntryIsTheMainScript: the common "am I the main script?" idiom
// counts stack levels, so it only works when the entry runs at the depth of
// a script.
func TestBundle_EntryIsTheMainScript(t *testing.T) {
	t.Parallel()

	luatest.Differential(t, cliCase(luatest.Project{
		"main.lua": "local M = {}\n" +
			"function M.main() print(\"running main\") end\n" +
			"if not pcall(debug.getlocal, 4, 1) then M.main() end\n" +
			"return M\n",
	}))
}

// TestBundle_EntryReturnsEveryValue: a program run with dofile returns
// whatever its main chunk returns, nil in the middle included.
func TestBundle_EntryReturnsEveryValue(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	project := luatest.Project{"main.lua": "return 1, nil, 3\n"}
	bundle := bundleProject(t, project, "--entry", "main.lua")

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		orig, bun := hostRuns(t, interp, showResults, project, bundle)
		require.Equal(t, "3\t1\tnil\t3\n", orig.Stdout, "the original\n%s", orig)
		luatest.AssertSame(t, orig, bun)
	})
}

// TestBundle_TwoBundlesInOneProcess: bundles run with dofile in one process
// each run their own entry, however their entries are named, and running the
// same bundle twice runs its entry twice, as it does for the original.
func TestBundle_TwoBundlesInOneProcess(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	one := bundleProject(t, luatest.Project{"main.lua": "print(\"one\")\n"}, "--entry", "main.lua")
	two := bundleProject(t, luatest.Project{"main.lua": "print(\"two\")\n"}, "--entry", "main.lua")

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		dir := luatest.WriteProject(t, luatest.Project{
			"one/main.lua": "print(\"one\")\n",
			"two/main.lua": "print(\"two\")\n",
			"one.lua":      string(one),
			"two.lua":      string(two),
			"originals.lua": "dofile(\"one/main.lua\")\ndofile(\"two/main.lua\")\n" +
				"dofile(\"one/main.lua\")\n",
			"bundles.lua": "dofile(\"one.lua\")\ndofile(\"two.lua\")\ndofile(\"one.lua\")\n",
		})
		orig := luatest.Run(t, interp, dir, runOptions(), "originals.lua")
		bun := luatest.Run(t, interp, dir, runOptions(), "bundles.lua")
		require.Equal(t, "one\ntwo\none\n", orig.Stdout, "the originals\n%s", orig)
		luatest.AssertSame(t, orig, bun)
	})
}

// TestBundle_LibraryRequiredByItsOwnName: a library bundled into mylib.lua
// from an entry named mylib is loaded by require("mylib"): the bundle must
// not require its own name while that require is still running.
func TestBundle_LibraryRequiredByItsOwnName(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	library := "return { hello = function() return \"HELLO\" end }\n"
	bundle := bundleProject(t, luatest.Project{"mylib.lua": library}, "--entry", "mylib.lua")
	program := "print(require(\"mylib\").hello())\n"

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		origDir := luatest.WriteProject(t, luatest.Project{"main.lua": program, "mylib.lua": library})
		bundleDir := luatest.WriteProject(t, luatest.Project{"main.lua": program, "mylib.lua": string(bundle)})
		orig := luatest.Run(t, interp, origDir, runOptions(), "main.lua")
		bun := luatest.Run(t, interp, bundleDir, runOptions(), "main.lua")
		require.Equal(t, "HELLO\n", orig.Stdout, "the original\n%s", orig)
		luatest.AssertSame(t, orig, bun)
	})
}

// TestBundle_FallbackRunsTheEmbeddedEntry: with --fallback an on-disk module
// may replace an embedded one, but the program is the bundle's own entry
// even when the working directory has a main.lua.
func TestBundle_FallbackRunsTheEmbeddedEntry(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	bundle := bundleProject(t, luatest.Project{"src/main.lua": "print(\"the real app\")\n"},
		"--entry", "src/main.lua", "--fallback")

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		dir := luatest.WriteProject(t, luatest.Project{
			"main.lua": "print(\"some unrelated main.lua\")\n",
			"app.lua":  string(bundle),
		})
		r := luatest.Run(t, interp, dir, runOptions(), "app.lua")
		assert.Equal(t, 0, r.ExitCode, "%s", r)
		assert.Equal(t, "the real app\n", r.Stdout, "%s", r)
	})
}

// TestBundle_EntryRequiredByAModule: a module that requires the entry by name
// loads a second instance of it, as natively, while the program itself still
// runs the entry as its main script.
func TestBundle_EntryRequiredByAModule(t *testing.T) {
	t.Parallel()

	luatest.Differential(t, cliCase(luatest.Project{
		"main.lua": "RUNS = (RUNS or 0) + 1\n" +
			"local M = { value = 42 }\n" +
			"function M.run() print(require(\"lib\").describe(), \"runs=\" .. RUNS) end\n" +
			"if not pcall(debug.getlocal, 4, 1) then M.run() end\n" +
			"return M\n",
		"lib.lua": "local main = require(\"main\")\n" +
			"return { describe = function() return \"main.value = \" .. main.value end }\n",
	}))
}

// TestBundle_EntryPathNameIsFree: the name derived from the entry's path only
// labels the bundle, so another file may be the module of that name: here
// require("main") finds src/main.lua, natively and in the bundle.
func TestBundle_EntryPathNameIsFree(t *testing.T) {
	t.Parallel()

	spec := cliCase(luatest.Project{
		"main.lua":     "print(\"entry\")\nprint((require(\"main\")))\n",
		"src/main.lua": "return \"src/main.lua\"\n",
	}, "--path", "src/?.lua")
	spec.Options.LuaPath = "./src/?.lua"
	spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
		t.Helper()

		if strings.HasPrefix(orig.Interp.Version, "Tarantool") {
			// Tarantool searches the directory of the script before
			// package.path, so natively require("main") loads the entry
			// itself there. The bundle's preload comes first.
			assert.Equal(t, 0, bundle.ExitCode, "the bundle\n%s", bundle)
			assert.Equal(t, "entry\nsrc/main.lua\n", bundle.Stdout, "the bundle\n%s", bundle)

			return
		}

		require.Equal(t, "entry\nsrc/main.lua\n", orig.Stdout, "the original\n%s", orig)
		luatest.AssertSame(t, orig, bundle)
	}

	luatest.Differential(t, spec)
}

// modes are the ways of emitting module and entry code, as CLI flags.
func modes() map[string][]string {
	return map[string][]string{"default": nil, "debug": {"--debug"}}
}

// TestBundle_Suffix: the suffix runs after the program, whatever the entry's
// last statement is. The original has no suffix, so the bundle prints what
// the original prints and then what the suffix prints.
func TestBundle_Suffix(t *testing.T) {
	t.Parallel()

	for mode, flags := range modes() {
		for name, entry := range map[string]string{
			"statement": "print(\"main\")\n",
			"return":    "print(\"main\")\nreturn \"main's result\"\n",
		} {
			t.Run(mode+"/"+name, func(t *testing.T) {
				t.Parallel()

				spec := cliCase(luatest.Project{"main.lua": entry},
					append([]string{"--suffix", "print(\"suffix\")"}, flags...)...)
				spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
					t.Helper()

					assert.Equal(t, "main\n", orig.Stdout, "the original\n%s", orig)
					assert.Equal(t, orig.Stdout+"suffix\n", bundle.Stdout, "the bundle\n%s", bundle)
					luatest.AssertSameExitCode(t, orig, bundle)
					luatest.AssertSameStderr(t, orig, bundle)
				}

				luatest.Differential(t, spec)
			})
		}
	}
}

// TestBundle_SuffixKeepsReturnValues: with a suffix the bundle still returns
// every value the entry returns, nil in the middle included.
func TestBundle_SuffixKeepsReturnValues(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	project := luatest.Project{"main.lua": "return 1, nil, 3\n"}

	for mode, flags := range modes() {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			bundle := bundleProject(t, project,
				append([]string{"--entry", "main.lua", "--suffix", "print(\"suffix\")"}, flags...)...)

			luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
				t.Helper()

				orig, bun := hostRuns(t, interp, showResults, project, bundle)
				require.Equal(t, "3\t1\tnil\t3\n", orig.Stdout, "the original\n%s", orig)
				assert.Equal(t, "suffix\n"+orig.Stdout, bun.Stdout, "the bundle\n%s", bun)
				luatest.AssertSameExitCode(t, orig, bun)
			})
		})
	}
}

// TestBundle_FailedLoadIsNotCached: after a module raised while loading, a
// second require of it fails again, as natively (Lua 5.1, LuaJIT and Tarantool
// report the previous error), so the common `pcall(require, ...)` feature
// check keeps its answer.
func TestBundle_FailedLoadIsNotCached(t *testing.T) {
	t.Parallel()

	spec := cliCase(luatest.Project{
		"main.lua": "print(\"first: \", pcall(require, \"flaky\"))\n" +
			"print(\"second:\", pcall(require, \"flaky\"))\n",
		"flaky.lua": "error(\"init failed\")\n",
	})
	spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
		t.Helper()

		// The caught messages carry positions: ./flaky.lua:1 natively,
		// bundle.lua:<line> in the bundle.
		assert.Equal(t, luatest.StripPositions(orig.Stdout), luatest.StripPositions(bundle.Stdout),
			"what the two requires returned (positions stripped)\n=== original: %s=== bundle: %s", orig, bundle)
		luatest.AssertSameExitCode(t, orig, bundle)
		luatest.AssertSameStderr(t, orig, bundle)
	}

	luatest.Differential(t, spec)
}

// assertFailsLikeACycle asserts that the bundle fails on a require cycle the
// way the original does. Lua 5.1, LuaJIT and Tarantool mark a module in
// package.loaded while it loads, and a require that finds the mark reports
// the loop: the messages must agree. Where an interpreter keeps no mark, the
// cycle recurses until the C stack is exhausted, and where that is noticed
// decides the message: a file searcher's loader adds "error loading module
// ... from file". There only the exhaustion is asserted.
func assertFailsLikeACycle(t *testing.T, orig, bundle luatest.Result) {
	t.Helper()

	require.NotEqual(t, 0, orig.ExitCode, "the original must fail on the cycle\n%s", orig)
	assert.NotEqual(t, 0, bundle.ExitCode, "the bundle must fail on the cycle too\n%s", bundle)
	luatest.AssertSameStdout(t, orig, bundle)

	if strings.Contains(orig.Message(), "loop or previous error loading module") {
		luatest.AssertSameStderr(t, orig, bundle)

		return
	}

	exhausted := `C stack overflow|too many C levels`
	assert.Regexp(t, exhausted, orig.Message(), "the original\n%s", orig)
	assert.Regexp(t, exhausted, bundle.Message(), "the bundle\n%s", bundle)
}

// TestBundle_RequireCycleFails: a require cycle fails natively on every Lua,
// so it fails in the bundle too, instead of handing the module that is still
// loading a placeholder value.
func TestBundle_RequireCycleFails(t *testing.T) {
	t.Parallel()

	spec := cliCase(luatest.Project{
		"main.lua": "local a = require(\"a\")\nprint(\"main: a.name =\", a.name)\n",
		"a.lua":    "local b = require(\"b\")\nreturn { name = \"a\" }\n",
		"b.lua":    "local a = require(\"a\")\nprint(\"b sees a =\", a)\nreturn {}\n",
	})
	spec.Check = assertFailsLikeACycle

	luatest.Differential(t, spec)
}

// flagSets are the modes that change how module bodies are emitted: the
// default, without the arg line, and debug, which loads each body as a chunk
// of its own.
func flagSets() map[string][]string {
	return map[string][]string{"default": nil, "no-arg-fix": {"--no-arg-fix"}, "debug": {"--debug"}}
}

// TestBundle_GlobalNameIsNotShadowed: a module reads and writes the globals
// of the program, whatever their names, not locals of the bundle's wrapper.
func TestBundle_GlobalNameIsNotShadowed(t *testing.T) {
	t.Parallel()

	for name, flags := range flagSets() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			luatest.Differential(t, cliCase(luatest.Project{
				"main.lua": "name = \"GLOBAL\"\nprint((require(\"m\")))\nprint(name)\n",
				"m.lua":    "local seen = name\n" + "name = \"set by m\"\nreturn seen\n",
			}, flags...))
		})
	}
}

// localsModule is a module that declares n locals at its top level and
// returns the last one.
func localsModule(n int) string {
	var source strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&source, "local v%d = %d\n", i, i)
	}

	fmt.Fprintf(&source, "return v%d\n", n)

	return source.String()
}

// varargFunctionHolds reports whether interp compiles a vararg function that
// declares n locals at its top level. A module body runs in one, while a
// module file is compiled as a main chunk, and PUC Lua 5.1 (the implicit
// `arg` of LUA_COMPAT_VARARG) keeps one local of its own in every vararg
// function but not in a main chunk.
func varargFunctionHolds(t *testing.T, interp luatest.Interpreter, n int) bool {
	t.Helper()

	dir := luatest.WriteProject(t, luatest.Project{
		"function.lua": "return function(...)\n" + localsModule(n) + "end\n",
		"probe.lua":    "print(loadfile(\"function.lua\") and \"fits\" or \"too many\")\n",
	})
	r := luatest.Run(t, interp, dir, runOptions(), "probe.lua")
	require.Equal(t, 0, r.ExitCode, "%s", r)

	return r.Stdout == "fits\n"
}

// TestBundle_ModuleWithManyLocals: the wrapper around a module body declares
// no local of its own, so a module holds as many top-level locals as a
// vararg function can: 200, the limit, natively, and on the interpreters
// whose vararg functions keep a local of their own (PUC Lua 5.1) one less,
// which a module with 200 locals then exceeds.
func TestBundle_ModuleWithManyLocals(t *testing.T) {
	t.Parallel()

	for _, n := range []int{199, 200} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			t.Parallel()

			spec := cliCase(luatest.Project{
				"main.lua": "print((require(\"m\")))\n",
				"m.lua":    localsModule(n),
			})
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				require.Equal(t, strconv.Itoa(n)+"\n", orig.Stdout, "the original\n%s", orig)

				if varargFunctionHolds(t, orig.Interp, n) {
					luatest.AssertSame(t, orig, bundle)

					return
				}

				assert.NotEqual(t, 0, bundle.ExitCode, "the bundle\n%s", bundle)
				assert.Contains(t, bundle.Message(), "local variables", "the bundle\n%s", bundle)
			}

			luatest.Differential(t, spec)
		})
	}
}

// TestBundle_ModuleStartsWithParenthesis: a module whose first statement
// starts with `(` is a statement of its own, not the continuation of a
// call on whatever the wrapper put before it.
func TestBundle_ModuleStartsWithParenthesis(t *testing.T) {
	t.Parallel()

	for name, flags := range flagSets() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			luatest.Differential(t, cliCase(luatest.Project{
				"main.lua": "require(\"m\")\nprint(\"main done\")\n",
				"m.lua":    "(function() print(\"iife ran\") end)()\n",
			}, flags...))
		})
	}
}

// isPUCLua51 reports whether interp is PUC Lua 5.1, not LuaJIT or Tarantool,
// which report the same _VERSION.
func isPUCLua51(interp luatest.Interpreter) bool {
	return interp.Version == "Lua 5.1"
}

// TestBundle_ArgAssignmentStaysGlobal: a program that replaces the global
// arg, e.g. to drop the options it consumed, replaces it for every module
// that reads arg later.
func TestBundle_ArgAssignmentStaysGlobal(t *testing.T) {
	t.Parallel()

	reader := "print(\"reader sees: \" .. tostring(arg[1]))\n"
	projects := map[string]luatest.Project{
		"entry": {
			"main.lua":   "arg = { \"patched\" }\nrequire(\"reader\")\n",
			"reader.lua": reader,
		},
		"module": {
			"main.lua":    "require(\"patcher\")\nrequire(\"reader\")\n",
			"patcher.lua": "arg = { \"patched\" }\n",
			"reader.lua":  reader,
		},
	}

	for mode, flags := range modes() {
		for name, project := range projects {
			t.Run(mode+"/"+name, func(t *testing.T) {
				t.Parallel()

				spec := cliCase(project, flags...)
				spec.Args = []string{"x"}
				spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
					t.Helper()

					require.Equal(t, "reader sees: patched\n", orig.Stdout, "the original\n%s", orig)

					if name == "module" && mode == "default" && isPUCLua51(orig.Interp) {
						// Known and documented: on PUC Lua 5.1 a module body
						// runs in a vararg function, and an assignment to arg
						// there sets the function's implicit arg local.
						assert.Equal(t, "reader sees: x\n", bundle.Stdout, "the bundle\n%s", bundle)

						return
					}

					luatest.AssertSame(t, orig, bundle)
				}

				luatest.Differential(t, spec)
			})
		}
	}
}

// TestBundle_ModuleReadsCommandLineArguments: a module reads the
// command-line arguments from the global arg, which on PUC Lua 5.1 the
// implicit `arg` local of the function around its body would hide; and so
// does the entry, which runs in such a function when there is a suffix.
func TestBundle_ModuleReadsCommandLineArguments(t *testing.T) {
	t.Parallel()

	variants := map[string][]string{"suffix": {"--suffix", "-- a suffix that does nothing"}}
	maps.Copy(variants, modes())

	for name, flags := range variants {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			spec := cliCase(luatest.Project{
				"main.lua":   "print(\"main sees: \" .. tostring(arg[1]), #arg)\nrequire(\"reader\")\n",
				"reader.lua": "print(\"reader sees: \" .. tostring(arg[1]), #arg)\n",
			}, flags...)
			spec.Args = []string{"x"}
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				require.Equal(t, "main sees: x\t1\nreader sees: x\t1\n", orig.Stdout, "the original\n%s", orig)
				luatest.AssertSame(t, orig, bundle)
			}

			luatest.Differential(t, spec)
		})
	}
}

// TestBundle_KeepsHostPreload: a module the embedding application registered
// in package.preload before running the program wins over the file on
// package.path natively, and so it does over the embedded copy.
func TestBundle_KeepsHostPreload(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	project := luatest.Project{
		"main.lua":  "print(\"codec impl: \" .. require(\"codec\").impl)\n",
		"codec.lua": "return { impl = \"bundled pure-Lua fallback\" }\n",
	}
	host := "package.preload[\"codec\"] = function() return { impl = \"host\" } end\n" +
		"dofile(arg[1])\n"

	for mode, flags := range modes() {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			bundle := bundleProject(t, project, append([]string{"--entry", "main.lua"}, flags...)...)

			luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
				t.Helper()

				orig, bun := hostRuns(t, interp, host, project, bundle)
				require.Equal(t, "codec impl: host\n", orig.Stdout, "the original\n%s", orig)
				luatest.AssertSame(t, orig, bun)
			})
		})
	}
}

// TestBundle_KeepsBuiltinPreload: LuaJIT and Tarantool provide table.new in
// package.preload. A project that carries a pure-Lua shim for the other
// interpreters gets the built-in one there natively, and so it does from the
// bundle.
func TestBundle_KeepsBuiltinPreload(t *testing.T) {
	t.Parallel()

	luatest.Differential(t, cliCase(luatest.Project{
		"main.lua": "local new_tab = require(\"table.new\")\n" +
			"print(\"table.new is \" .. debug.getinfo(new_tab, \"S\").what)\n",
		"table/new.lua": "return function(narr, nrec) return {} end\n",
	}))
}

// TestBundle_FallbackKeepsRegisteredLoaders: with --fallback the bundle
// registers its modules in package.postload, and keeps a loader somebody
// registered there first, as it keeps one in package.preload.
func TestBundle_FallbackKeepsRegisteredLoaders(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	bundle := bundleProject(t, luatest.Project{
		"main.lua":  "print(\"codec impl: \" .. require(\"codec\").impl)\n",
		"codec.lua": "return { impl = \"bundled pure-Lua fallback\" }\n",
	}, "--entry", "main.lua", "--fallback")

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		dir := luatest.WriteProject(t, luatest.Project{
			"host.lua": "package.postload = { codec = function() return { impl = \"registered first\" } end }\n" +
				"dofile(\"bundle.lua\")\n",
			"bundle.lua": string(bundle),
		})
		r := luatest.Run(t, interp, dir, runOptions(), "host.lua")
		assert.Equal(t, 0, r.ExitCode, "%s", r)
		assert.Equal(t, "codec impl: registered first\n", r.Stdout, "%s", r)
	})
}

// TestBundle_LoaderData pins the loader data of an embedded module, as the
// README's Limitations describe it. Lua 5.1, LuaJIT and Tarantool give a
// module nothing but its name, and require returns nothing but the module's
// value, natively and in a bundle alike, also with --fallback and under the
// name of a runtime's built-in module, which the bundle serves from its
// wrapper around the first searcher.
func TestBundle_LoaderData(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		flags []string
		// name is the name the module is required by, and of its file.
		name string
	}{
		"default":  {flags: nil, name: "m"},
		"debug":    {flags: []string{"--debug"}, name: "m"},
		"fallback": {flags: []string{"--fallback"}, name: "m"},
		// Tarantool's own csv, which require loads there instead, is not
		// this test's business.
		"built-in name": {flags: nil, name: "csv"},
	}

	for variant, tt := range tests {
		t.Run(variant, func(t *testing.T) {
			t.Parallel()

			spec := cliCase(luatest.Project{
				"main.lua": "local function show(...) print(\"require returns\", select(\"#\", ...), ...) end\n" +
					"show(require(\"" + tt.name + "\"))\n",
				tt.name + ".lua": "print(\"module gets\", select(\"#\", ...), ...)\nreturn \"M\"\n",
			}, tt.flags...)
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				if tt.name == "csv" && strings.HasPrefix(orig.Interp.Version, "Tarantool") {
					t.Skip("Tarantool has a csv module of its own")
				}

				want := "module gets\t1\t" + tt.name + "\nrequire returns\t1\tM\n"
				require.Equal(t, want, orig.Stdout, "the original\n%s", orig)
				assert.Equal(t, want, bundle.Stdout, "the bundle\n%s", bundle)
				luatest.AssertSameExitCode(t, orig, bundle)
				luatest.AssertSameStderr(t, orig, bundle)
			}

			luatest.Differential(t, spec)
		})
	}
}

// TestBundle_FallbackReportsAMissingModule: with --fallback, the error for a
// module found nowhere is the interpreter's own plus a line for the bundle's
// searcher. Lua 5.1, LuaJIT and Tarantool expect a searcher to start its
// message with "\n\t".
//
// The program prints the message itself: Tarantool cuts an uncaught error's
// message short, and here it lists the files it tried in every directory up
// to the root of the file system. The searcher lines are looked at as
// printed, since NormalizeStderr drops them.
func TestBundle_FallbackReportsAMissingModule(t *testing.T) {
	t.Parallel()

	spec := cliCase(luatest.Project{
		"main.lua": "print((require(\"m\")))\n" +
			"local _, err = pcall(require, \"missing\")\n" +
			"io.stderr:write(err, \"\\n\")\n" +
			"os.exit(1)\n",
		"m.lua": "return \"m\"\n",
	}, "--fallback")
	spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
		t.Helper()

		require.Equal(t, 1, orig.ExitCode, "the original\n%s", orig)
		require.Contains(t, orig.Message(), "module 'missing' not found", "the original\n%s", orig)
		luatest.AssertSame(t, orig, bundle)

		for _, r := range []luatest.Result{orig, bundle} {
			for i, line := range strings.Split(strings.TrimSuffix(r.Stderr, "\n"), "\n") {
				assert.NotEmpty(t, strings.TrimSpace(line), "line %d of the message is blank\n%s", i+1, r)
			}
		}

		lines := strings.Split(bundle.Stderr, "\n")
		assert.Contains(t, lines, "\tno field package.postload['missing']",
			"the searcher's message is a line of its own\n%s", bundle)
	}

	luatest.Differential(t, spec)
}

// strictGlobals makes reading or assigning an undeclared global raise, as
// strict modes such as Tarantool's `strict` or Lua's etc/strict.lua do.
const strictGlobals = "setmetatable(_G, {\n" +
	"  __index = function(_, name) error(\"undeclared global '\" .. tostring(name) .. \"'\", 2) end,\n" +
	"  __newindex = function(_, name)\n" +
	"    error(\"assignment to undeclared global '\" .. tostring(name) .. \"'\", 2)\n" +
	"  end,\n" +
	"})\n"

// TestBundle_StrictGlobals: a program that turns strict mode on before its
// first require works in a bundle whose prefix does the same, in every mode:
// the bundle's own code reads no global that does not exist.
func TestBundle_StrictGlobals(t *testing.T) {
	t.Parallel()

	variants := map[string][]string{
		"fallback": {"--fallback"},
		"suffix":   {"--suffix", "-- a suffix that does nothing"},
	}
	maps.Copy(variants, modes())

	for name, flags := range variants {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			luatest.Differential(t, cliCase(luatest.Project{
				"main.lua": strictGlobals + "local m = require(\"m\")\nprint(m.v)\n",
				"m.lua":    "return { v = \"ok\" }\n",
			}, append([]string{"--prefix", strictGlobals}, flags...)...))
		})
	}
}

// TestBundle_StrictModeExample is the README's example of startup and
// teardown code: Tarantool's strict mode turned on in the prefix, a message
// in the suffix. It runs where require("strict") works natively.
func TestBundle_StrictModeExample(t *testing.T) {
	t.Parallel()

	spec := cliCase(luatest.Project{
		"main.lua": "require(\"strict\").on()\nlocal m = require(\"m\")\nprint(m.v)\n",
		"m.lua":    "return { v = \"ok\" }\n",
	}, "--prefix", "require(\"strict\").on()", "--suffix", "print(\"bundle loaded\")")
	spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
		t.Helper()

		if orig.ExitCode != 0 && strings.Contains(orig.Message(), "module 'strict' not found") {
			t.Skipf("%s has no strict module", orig.Interp.Name)
		}

		require.Equal(t, "ok\n", orig.Stdout, "the original\n%s", orig)
		assert.Equal(t, orig.Stdout+"bundle loaded\n", bundle.Stdout, "the bundle\n%s", bundle)
		luatest.AssertSameExitCode(t, orig, bundle)
		luatest.AssertSameStderr(t, orig, bundle)
	}

	luatest.Differential(t, spec)
}

// TestBundle_ArgLineUnderStrictGlobals: the arg line reads the global arg on
// PUC Lua 5.1, where an embedding application may run the program with no
// global arg and a strict _G, in which reading a missing global raises.
func TestBundle_ArgLineUnderStrictGlobals(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	project := luatest.Project{
		"main.lua": "print((require(\"m\")))\n",
		"m.lua":    "return \"m loaded\"\n",
	}
	host := "local program = arg[1]\narg = nil\n" + strictGlobals + "dofile(program)\n"
	variants := map[string][]string{"suffix": {"--suffix", "-- a suffix that does nothing"}}
	maps.Copy(variants, modes())

	for name, flags := range variants {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bundle := bundleProject(t, project, append([]string{"--entry", "main.lua"}, flags...)...)

			luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
				t.Helper()

				orig, bun := hostRuns(t, interp, host, project, bundle)
				require.Equal(t, "m loaded\n", orig.Stdout, "the original\n%s", orig)
				luatest.AssertSame(t, orig, bun)
			})
		})
	}
}

// TestBundle_PrefixHasItsOwnScope: the prefix is code of its own. Its locals
// are not visible to the modules or to the entry, in either mode, and its
// last statement does not run on into the program's first one.
func TestBundle_PrefixHasItsOwnScope(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		prefix  string
		project luatest.Project
	}{
		"local": {
			prefix: "local helper = \"prefix local\"",
			project: luatest.Project{
				"main.lua": "print(\"main sees \" .. tostring(helper))\nprint((require(\"m\")))\n",
				"m.lua":    "return \"m sees \" .. tostring(helper)\n",
			},
		},
		"trailing expression": {
			// Glued to the entry, `print` would call the function and then
			// the nothing print returns.
			prefix:  "handler = print",
			project: luatest.Project{"main.lua": "(function() print(\"entry ran\") end)()\n"},
		},
	}

	for mode, flags := range modes() {
		for name, tt := range cases {
			t.Run(mode+"/"+name, func(t *testing.T) {
				t.Parallel()

				luatest.Differential(t, cliCase(tt.project, append([]string{"--prefix", tt.prefix}, flags...)...))
			})
		}
	}
}

// TestBundle_ModulesInheritTheEnvironment: the modules of a bundle run in the
// environment the bundle was loaded with, in both modes. That is a documented
// difference from native modules, which require loads with the global
// environment.
func TestBundle_ModulesInheritTheEnvironment(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	project := luatest.Project{
		"main.lua": "print(\"main\")\nrequire(\"m\").hello()\n",
		"m.lua":    "return { hello = function() print(\"module\") end }\n",
	}
	// A host that gives the program it runs its own print.
	host := "local env = setmetatable({ print = function(...) print(\"[plugin]\", ...) end }, { __index = _G })\n" +
		"local f = assert(loadfile(arg[1]))\n" +
		"if setfenv then setfenv(f, env) else f = assert(loadfile(arg[1], \"t\", env)) end\n" +
		"f()\n"
	suffix := []string{"--suffix", "-- a suffix that does nothing"}
	variants := map[string][]string{
		"default": nil, "debug": {"--debug"}, "suffix": suffix, "debug suffix": append([]string{"--debug"}, suffix...),
	}
	bundles := make(map[string][]byte, len(variants))

	for name, flags := range variants {
		bundles[name] = bundleProject(t, project, append([]string{"--entry", "main.lua"}, flags...)...)
	}

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		for name, bundle := range bundles {
			orig, bun := hostRuns(t, interp, host, project, bundle)
			require.Equal(t, "[plugin]\tmain\nmodule\n", orig.Stdout, "the original\n%s", orig)
			assert.Equal(t, "[plugin]\tmain\n[plugin]\tmodule\n", bun.Stdout, "the %s bundle\n%s", name, bun)
			luatest.AssertSameExitCode(t, orig, bun)
		}
	})
}

// TestBundle_EachNameIsAnInstance: natively two require names that find the
// same file are two modules: package.loaded caches each name, so the file
// runs once per name and each run sees its own name in `...`.
func TestBundle_EachNameIsAnInstance(t *testing.T) {
	t.Parallel()

	main := "local a = require(\"%s\")\nlocal b = require(\"%s\")\n" +
		"print(\"runs=\" .. RUNS, \"same=\" .. tostring(a == b), a.name, b.name)\n"
	counter := "RUNS = (RUNS or 0) + 1\nreturn { name = (...) }\n"

	for name, tt := range map[string]struct{ first, second, file string }{
		"path-style name": {first: "counter", second: "./counter", file: "counter.lua"},
		"init module":     {first: "counter", second: "counter.init", file: "counter/init.lua"},
	} {
		for mode, flags := range modes() {
			t.Run(name+"/"+mode, func(t *testing.T) {
				t.Parallel()

				spec := cliCase(luatest.Project{
					"main.lua": fmt.Sprintf(main, tt.first, tt.second),
					tt.file:    counter,
				}, flags...)
				spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
					t.Helper()

					want := fmt.Sprintf("runs=2\tsame=false\t%s\t%s\n", tt.first, tt.second)
					require.Equal(t, want, orig.Stdout, "the original\n%s", orig)
					luatest.AssertSame(t, orig, bundle)
				}

				luatest.Differential(t, spec)
			})
		}
	}
}
