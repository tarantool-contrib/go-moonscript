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

// TestCLI_RuntimeBuiltinWithoutAFile: a module that some runtimes have built
// in, and the project has no file for, is left to the runtime without a
// warning, even with --strict. The bundle loads the built-in where the
// original does and fails like the original elsewhere.
func TestCLI_RuntimeBuiltinWithoutAFile(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"utf8", "bit", "csv"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			luatest.Differential(t, differential(luatest.Project{
				"main.lua": "local m = require(\"" + name + "\")\nprint(type(m))\n",
			}, "", testCLIBundler(nil, "--entry", "main.lua", "--strict")))
		})
	}
}

// TestCLI_ProjectModuleNamedLikeABuiltin: natively, where the runtime has a
// built-in module of the name a module of the project is required by,
// require loads the built-in and never the file; elsewhere it loads the file.
// The bundle must do the same on every interpreter, in every mode, and the
// CLI warns about each such name.
func TestCLI_ProjectModuleNamedLikeABuiltin(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		project  luatest.Project
		warnings []string
		// tarantool is what the original prints on Tarantool, which proves
		// the case: the file of the project is not what it loads there.
		tarantool string
	}{
		// Tarantool finds its csv with a searcher that runs after
		// package.preload.
		"csv": {
			project: luatest.Project{
				"main.lua": "local csv = require(\"csv\")\n" +
					"print(type(csv.load) == \"function\" and \"tarantool built-in csv\" or csv.name)\n",
				"csv.lua": "return { name = \"project ./csv.lua\" }\n",
			},
			warnings:  []string{`main\.lua:1: require "csv": Tarantool loads its built-in module "csv"`},
			tarantool: "tarantool built-in csv\n",
		},
		// Pure-Lua fallbacks for LuaJIT's bit (in package.loaded) and
		// table.new (in package.preload).
		"luajit fallbacks": {
			project: luatest.Project{
				"main.lua": "local bit = require(\"bit\")\nlocal new_tab = require(\"table.new\")\n" +
					"print(\"bit is \" .. (bit.impl or \"built-in\"), " +
					"\"table.new is \" .. debug.getinfo(new_tab, \"S\").what)\n",
				"bit.lua":       "return { impl = \"project bit.lua\", band = function(a, b) return 0 end }\n",
				"table/new.lua": "return function(narr, nrec) return {} end\n",
			},
			warnings: []string{
				`main\.lua:1: require "bit": LuaJIT and Tarantool load their built-in module "bit"`,
				`main\.lua:2: require "table\.new": LuaJIT and Tarantool load their built-in`,
			},
			tarantool: "bit is built-in\ttable.new is C\n",
		},
		// A utf8.lua for Lua 5.1 and 5.2.
		"utf8": {
			project: luatest.Project{
				"main.lua": "print(\"utf8 is \" .. (require(\"utf8\").impl or \"built-in\"))\n",
				"utf8.lua": "return { impl = \"project utf8.lua\" }\n",
			},
			warnings: []string{
				`main\.lua:1: require "utf8": Lua 5\.3, Lua 5\.4, Lua 5\.5 and Tarantool load their`,
			},
			tarantool: "utf8 is built-in\n",
		},
	}

	for mode, flags := range map[string][]string{"default": nil, "debug": {"--debug"}, "fallback": {"--fallback"}} {
		for name, tt := range tests {
			t.Run(mode+"/"+name, func(t *testing.T) {
				t.Parallel()

				args := append([]string{"--entry", "main.lua", "--strict"}, flags...)
				spec := differential(tt.project, "", testCLIBundler(tt.warnings, args...))
				spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
					t.Helper()

					require.Equal(t, 0, orig.ExitCode, "the original\n%s", orig)

					if strings.HasPrefix(orig.Interp.Version, "Tarantool") {
						require.Equal(t, tt.tarantool, orig.Stdout, "the original\n%s", orig)
					}

					luatest.AssertSame(t, orig, bundle)
				}

				luatest.Differential(t, spec)
			})
		}
	}
}
