package integration_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/go-luamerge/internal/luatest"
)

// TestCLI_LocalNamedRequire: a local named require that is a function of its
// own hides the global one, so the calls through it load no module. The
// bundler took them for requires: --strict failed with `unresolved require
// "greeting"` on a program that runs, and a file of that name was read and
// embedded although the program never loads it; one the interpreter cannot
// compile failed the build. The global require is found again where the
// local's scope ends. A local that wraps the global require, such as a lazy
// require, loads the modules of the names it gets, so they are embedded.
func TestCLI_LocalNamedRequire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		project  luatest.Project
		warnings []string
		stdout   string
	}{
		{
			name: "local function",
			project: luatest.Project{
				"main.lua": "local require = function(x) return \"local \" .. x end\n" +
					"print(require(\"greeting\"))\n",
			},
			warnings: nil,
			stdout:   "local greeting\n",
		},
		{
			name: "local function and a file of its name",
			project: luatest.Project{
				"main.lua": "local require = function(x) return \"local \" .. x end\n" +
					"print(require(\"greeting\"))\n",
				"greeting.lua": "this is not Lua\n",
			},
			warnings: nil,
			stdout:   "local greeting\n",
		},
		{
			name: "parameter, loop variable, nested function and block",
			project: luatest.Project{
				"main.lua": "local function with(require) return require(\"p\") end\n" +
					"print(with(function(name) return \"parameter \" .. name end))\n" +
					"for _, require in ipairs({ function(name) return \"loop \" .. name end }) do\n" +
					"  print(require(\"l\"))\n" +
					"end\n" +
					"local function nested()\n" +
					"  local require = function(name) return \"nested \" .. name end\n" +
					"  return require(\"n\")\n" +
					"end\n" +
					"print(nested())\n" +
					"do\n" +
					"  local function require(name) return \"block \" .. name end\n" +
					"  print(require(\"b\"))\n" +
					"end\n" +
					"print((require(\"m\")))\n",
				"m.lua": "return \"module m\"\n",
			},
			warnings: nil,
			stdout:   "parameter p\nloop l\nnested n\nblock b\nmodule m\n",
		},
		{
			name: "lazy require",
			project: luatest.Project{
				"main.lua": "local require = require(\"lazy\")\n" +
					"local m = require(\"m\")\n" +
					"print(m.value)\n",
				"lazy.lua": "return function(name)\n" +
					"  return setmetatable({}, { __index = function(_, key) return require(name)[key] end })\n" +
					"end\n",
				"m.lua": "return { value = \"loaded lazily\" }\n",
			},
			warnings: []string{`lazy\.lua:2: dynamic require`},
			stdout:   "loaded lazily\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := differential(tt.project, "", testCLIBundler(tt.warnings, "--entry", "main.lua", "--strict"))
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				luatest.AssertSame(t, orig, bundle)
				assert.Equal(t, tt.stdout, orig.Stdout, "stdout of the original\n%s", orig)
			}

			luatest.Differential(t, spec)
		})
	}
}
