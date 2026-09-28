package graph_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/luatest"
)

// testProbeScript is a Lua script that reports, for each of names, whether
// require finds it without a file: with every searcher but the first removed,
// require has nothing left but package.loaded and the first searcher, which
// is the preload searcher (on Tarantool also its override and built-in
// module searchers). It runs from a directory that holds nothing else, so no
// override module can be found either.
func testProbeScript(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}

	return "local searchers = package.searchers or package.loaders\n" +
		"for i = #searchers, 2, -1 do searchers[i] = nil end\n" +
		"for _, name in ipairs({ " + strings.Join(quoted, ", ") + " }) do\n" +
		"  print((pcall(require, name) and \"provided \" or \"missing \") .. name)\n" +
		"end\n"
}

// testProvided runs testProbeScript for names on interp and returns the names
// the interpreter provides without a file.
func testProvided(t *testing.T, interp luatest.Interpreter, names []string) map[string]bool {
	t.Helper()

	dir := luatest.WriteProject(t, luatest.Project{"probe.lua": testProbeScript(names)})
	r := luatest.Run(t, interp, dir, luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}, "probe.lua")
	require.Equal(t, 0, r.ExitCode, "the probe\n%s", r)

	provided := make(map[string]bool)
	lines := strings.Split(strings.TrimSuffix(r.Stdout, "\n"), "\n")
	require.Len(t, lines, len(names), "one line per name\n%s", r)

	for _, line := range lines {
		name, ok := strings.CutPrefix(line, "provided ")
		if ok {
			provided[name] = true

			continue
		}

		_, ok = strings.CutPrefix(line, "missing ")
		require.True(t, ok, "unexpected probe output %q\n%s", line, r)
	}

	return provided
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

		provided := testProvided(t, interp, names)
		for _, name := range names {
			assert.True(t, provided[name], "%s does not provide %q without a file", interp, name)
		}
	})
}
