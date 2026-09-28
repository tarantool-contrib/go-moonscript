package integration_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/luatest"
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
// second require of it fails again, as natively (Lua 5.2 and later run the
// module again; Lua 5.1, LuaJIT and Tarantool report the previous error), so
// the common `pcall(require, ...)` feature check keeps its answer.
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
// the loop: the messages must agree. Lua 5.2 and later keep no mark, so the
// cycle recurses until the C stack is exhausted, and where that is noticed
// decides the message: a file searcher's loader adds "error loading module
// ... from file", and Lua 5.2 and 5.3 may run out of C levels in the parser
// ("too many C levels") before the stack overflows. There only the
// exhaustion is asserted.
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
