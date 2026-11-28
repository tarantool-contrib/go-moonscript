package graph_test

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/luatest"
)

// testProbeScript is a Lua script that reports what the interpreter running
// it provides without a file, in lines of the form "<what> <name>":
//
//   - "has <module>": every module in package.loaded and package.preload at
//     startup, and in Tarantool's table of built-in modules;
//   - "runtime <runtime>": the runtimes the interpreter is, named as the
//     graph package names them. A LuaJIT of 2021 or later is told from the
//     2017 release by its string buffer library, since their versions can
//     read the same, so string.buffer itself is not checked by this;
//   - "provided <module>" or "missing <module>", for each of names: whether
//     require finds it with every searcher but the first removed. That leaves
//     require nothing but package.loaded and the first searcher: the preload
//     searcher, and on Tarantool also its override and built-in module
//     searchers. The script runs from a directory that holds nothing else, so
//     no override module can be found either.
func testProbeScript(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}

	return `local function list(modules)
  for name in pairs(modules) do print("has " .. name) end
  return modules
end
local loaders = package.loaded["internal.loaders"]
list(package.loaded)
list(package.preload)
list(type(loaders) == "table" and loaders.builtin or {})
if rawget(_G, "_TARANTOOL") then
  print("runtime Tarantool")
  if tonumber(_TARANTOOL:match("^%d+")) >= 3 then print("runtime Tarantool 3") end
  if package.loaded.tarantool.build.linking == "static" then
    print("runtime Tarantool's static build")
  end
elseif rawget(_G, "jit") then
  print("runtime LuaJIT")
  if package.preload["string.buffer"] ~= nil then print("runtime LuaJIT since 2021") end
  if jit.prngstate ~= nil then print("runtime OpenResty's LuaJIT") end
else
  print("runtime " .. _VERSION)
end
local searchers = package.searchers or package.loaders
for i = #searchers, 2, -1 do searchers[i] = nil end
for _, name in ipairs({ ` + strings.Join(quoted, ", ") + ` }) do
  print((pcall(require, name) and "provided " or "missing ") .. name)
end
`
}

// testProbe is what testProbeScript reported.
type testProbe struct {
	// has are the modules the interpreter has without a file.
	has map[string]bool
	// runtimes are the runtimes the interpreter is.
	runtimes []string
	// provided are the names require found without a file.
	provided map[string]bool
}

// testRunProbe runs testProbeScript for names on interp.
func testRunProbe(t *testing.T, interp luatest.Interpreter, names []string) testProbe {
	t.Helper()

	dir := luatest.WriteProject(t, luatest.Project{"probe.lua": testProbeScript(names)})
	r := luatest.Run(t, interp, dir, luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}, "probe.lua")
	require.Equal(t, 0, r.ExitCode, "the probe\n%s", r)

	probe := testProbe{has: make(map[string]bool), runtimes: nil, provided: make(map[string]bool)}
	answered := 0

	for line := range strings.SplitSeq(strings.TrimSuffix(r.Stdout, "\n"), "\n") {
		what, name, ok := strings.Cut(line, " ")
		require.True(t, ok, "unexpected probe output %q\n%s", line, r)

		switch what {
		case "has":
			probe.has[name] = true
		case "runtime":
			probe.runtimes = append(probe.runtimes, name)
		case "provided":
			probe.provided[name] = true
			answered++
		case "missing":
			answered++
		default:
			require.Fail(t, "unexpected probe output", "%q\n%s", line, r)
		}
	}

	require.Equal(t, len(names), answered, "one answer per name\n%s", r)
	require.NotEmpty(t, probe.runtimes, "the probe names no runtime\n%s", r)

	for _, runtime := range probe.runtimes {
		require.Contains(t, graph.RuntimeNamesForTest(), runtime,
			"%s is a runtime the lists do not know: find out what it provides and add it", interp)
	}

	return probe
}

// TestStandardLibrary_EveryInterpreterProvidesIt: the bundler never looks for
// a file for a standard library module, which is right only if every
// interpreter has each of them without a file.
func TestStandardLibrary_EveryInterpreterProvidesIt(t *testing.T) {
	t.Parallel()

	names := graph.StandardLibraryForTest()
	require.NotEmpty(t, names)

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		probe := testRunProbe(t, interp, names)
		for _, name := range names {
			assert.True(t, probe.provided[name], "%s does not provide %q without a file", interp, name)
		}
	})
}

// TestBuiltinModules_MatchEveryInterpreter: the list of modules runtimes have
// built in decides which requires need no file and which modules of a
// project the runtime's own module takes precedence over. So on every
// interpreter present, a listed module must be provided without a file
// exactly when the list attributes it to one of the runtimes the interpreter
// is, and every module the interpreter has without a file must be listed with
// its runtime, so a new built-in cannot go unnoticed. The one exception is
// Tarantool 3, whose list is the union of what its releases have: a release
// may lack a module listed for it.
func TestBuiltinModules_MatchEveryInterpreter(t *testing.T) {
	t.Parallel()

	modules := graph.BuiltinModulesForTest()
	require.NotEmpty(t, modules)

	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}

	sort.Strings(names)

	// partial are the runtimes a release of which may lack a module listed
	// for them.
	partial := []string{"Tarantool 3"}

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		probe := testRunProbe(t, interp, names)
		// listedFor returns the runtimes the interpreter is that name is
		// listed for.
		listedFor := func(name string) []string {
			var runtimes []string

			for _, runtime := range modules[name] {
				if slices.Contains(probe.runtimes, runtime) {
					runtimes = append(runtimes, runtime)
				}
			}

			return runtimes
		}
		isPartial := func(runtime string) bool { return slices.Contains(partial, runtime) }

		var wrong, missing []string

		for _, name := range names {
			runtimes := listedFor(name)
			provided := probe.provided[name]
			// The runtimes whose every release must have the module.
			strict := slices.DeleteFunc(slices.Clone(runtimes), isPartial)

			if (provided && len(runtimes) == 0) || (!provided && len(strict) > 0) {
				wrong = append(wrong, fmt.Sprintf("%s (listed for %v, provided: %t)", name, modules[name], provided))
			}
		}

		for name := range probe.has {
			if !slices.Contains(graph.StandardLibraryForTest(), name) && len(listedFor(name)) == 0 {
				missing = append(missing, name)
			}
		}

		sort.Strings(missing)
		assert.Empty(t, wrong, "%s, runtimes %v: listed modules attributed wrongly", interp, probe.runtimes)
		assert.Empty(t, missing, "%s, runtimes %v: modules it has without a file that are not listed for it",
			interp, probe.runtimes)
	})
}
