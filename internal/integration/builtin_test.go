package integration_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/luatest"
)

// The tests in this file are about the modules a Lua runtime provides without
// a file: its standard library and its own built-in modules.

// TestCLI_StandardLibraryModules: a require of a standard library module,
// such as Penlight's require("debug"), is found in package.loaded natively,
// so --strict must not fail on it, and the bundle must behave the same.
func TestCLI_StandardLibraryModules(t *testing.T) {
	t.Parallel()

	spec := cliCase(luatest.Project{
		"main.lua": "local d = require(\"debug\")\nprint(type(d.traceback))\n" +
			"print(require(\"_G\") == _G, require(\"coroutine\") == coroutine, require(\"io\") == io,\n" +
			"  require(\"math\") == math, require(\"os\") == os, require(\"package\") == package,\n" +
			"  require(\"string\") == string, require(\"table\") == table)\n",
	}, "--strict")
	spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
		t.Helper()

		// The same module as the global of that name, for each of the eight.
		want := "function\ntrue" + strings.Repeat("\ttrue", 7) + "\n"
		require.Equal(t, want, orig.Stdout, "the original\n%s", orig)
		luatest.AssertSame(t, orig, bundle)
	}

	luatest.Differential(t, spec)
}
