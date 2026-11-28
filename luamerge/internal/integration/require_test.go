package integration_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/luatest"
)

// TestCLI_RequireAsMethodReceiver: a require whose result is the receiver of
// a method call was never found, so the bundle, run from an empty directory,
// failed with "module 'counter' not found" while the CLI exited 0 without a
// warning, even with --strict.
func TestCLI_RequireAsMethodReceiver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		project luatest.Project
		stdout  string
	}{
		{
			name: "constructor called on the module",
			project: luatest.Project{
				"main.lua": "local c = require(\"counter\"):new(5)\n" +
					"print(\"counter:\", c.n)\n",
				"counter.lua": "local C = {}\n" +
					"C.__index = C\n" +
					"function C:new(n) return setmetatable({ n = n }, self) end\n" +
					"return C\n",
			},
			stdout: "counter:\t5\n",
		},
		{
			// Module names must not collide with an interpreter's built-in
			// modules: Tarantool has its own `config`.
			name: "every receiver form",
			project: luatest.Project{
				"main.lua": "print((require \"greeting\"):upper())\n" +
					"print(require(\"settings\").db:describe())\n" +
					"require(\"app\"):use(require \"plugin_a\")" +
					":use(require \"plugin_b\"):listen()\n",
				"greeting.lua": "return \"hello\"\n",
				"settings.lua": "local db = { name = \"main\" }\n" +
					"function db:describe() return \"db \" .. self.name end\n" +
					"return { db = db }\n",
				"app.lua": "local app = { plugins = {} }\n" +
					"function app:use(p)\n" +
					"  self.plugins[#self.plugins + 1] = p.name\n" +
					"  return self\n" +
					"end\n" +
					"function app:listen()\n" +
					"  print(\"listening with \" .. table.concat(self.plugins, \", \"))\n" +
					"end\n" +
					"return app\n",
				"plugin_a.lua": "return { name = \"a\" }\n",
				"plugin_b.lua": "return { name = \"b\" }\n",
			},
			stdout: "HELLO\ndb main\nlistening with a, b\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := differential(tt.project, "", luatest.CLI("--entry", "main.lua", "--strict"))
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				luatest.AssertSame(t, orig, bundle)
				assert.Equal(t, tt.stdout, bundle.Stdout, "bundle stdout\n%s", bundle)
			}

			luatest.Differential(t, spec)
		})
	}
}

// testCLIBundler bundles through the CLI like luatest.CLI and additionally
// asserts that its stderr has a line matching each of the patterns in want,
// or no output at all when want is empty.
func testCLIBundler(want []string, args ...string) luatest.Bundler {
	return func(tb testing.TB, dir string) []byte {
		tb.Helper()

		cmd := "luamerge " + strings.Join(args, " ")
		r := luatest.RunCLI(tb, dir, nil, args...)
		require.Equal(tb, 0, r.ExitCode, "%s\nstderr:\n%s", cmd, r.Stderr)

		if len(want) == 0 {
			assert.Empty(tb, r.Stderr, cmd)
		}

		for _, pattern := range want {
			assert.Regexp(tb, `(?m)^.*`+pattern+`.*$`, r.Stderr, "%s: no warning matching %q", cmd, pattern)
		}

		require.NotEmpty(tb, r.Stdout, "%s wrote no bundle", cmd)

		return []byte(r.Stdout)
	}
}

// TestCLI_IndirectRequire: require reached other than by a plain call used to
// be dropped silently: no module, no warning, exit 0 even with --strict.
// `_G.require("m2")` and `xpcall(require, h, "m3")` name their module, so it is
// embedded. The alias `r`, `pcall(require, name)` and require handed to a
// function stay runtime requires and must be reported; with those modules
// added by --include the bundle behaves like the original.
//
// PUC Lua 5.1's xpcall passes no arguments to the function it calls, so there
// `xpcall(require, h, "m3")` fails natively; the handler returns a constant so
// that the original and the bundle print the same.
func TestCLI_IndirectRequire(t *testing.T) {
	t.Parallel()

	modules := func(p luatest.Project, names ...string) luatest.Project {
		for _, name := range names {
			p[name+".lua"] = "return \"" + name + "\"\n"
		}

		return p
	}
	tests := []struct {
		name     string
		project  luatest.Project
		args     []string
		warnings []string
	}{
		{
			// Every form that used to be dropped silently, printing what the
			// module returns; `print((f()))` keeps the loader data Lua 5.4+
			// returns after the module out of the output.
			name: "indirect forms",
			project: modules(luatest.Project{
				"main.lua": `local r = require; print((r("m1")))
print((_G.require("m2")))
local ok, m3 = xpcall(require, function() return "no arguments" end, "m3")
print(ok, m3)
local name = "m4"; print((select(2, pcall(require, name))))
local function load_all(req, names)
  local t = {}
  for i, n in ipairs(names) do t[i] = req(n) end
  return t
end
print(load_all(require, { "m5" })[1])
`,
			}, "m1", "m2", "m3", "m4", "m5"),
			args: []string{"--include", "m1", "--include", "m4", "--include", "m5"},
			warnings: []string{
				`main\.lua:1\b.*require aliased as local "r"`,
				`main\.lua:5\b.*dynamic require`,
				`main\.lua:11\b.*require used as a value`,
			},
		},
		{
			name: "cached global",
			project: modules(luatest.Project{
				"main.lua": "local require = require\nprint((require(\"m6\")))\n",
			}, "m6"),
			args:     nil,
			warnings: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := append([]string{"--entry", "main.lua", "--strict"}, tt.args...)
			spec := differential(tt.project, "", testCLIBundler(tt.warnings, args...))
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				luatest.AssertSame(t, orig, bundle)
				assert.Equal(t, 0, orig.ExitCode, "the original must run through\n%s", orig)
			}

			luatest.Differential(t, spec)
		})
	}
}

// TestCLI_ProtectedRequireOfAMissingModule: the optional-dependency idiom
// `pcall(require, "cjson")` runs fine without cjson, so --strict must not
// fail on it: the require is reported and left to runtime, and the bundle
// takes the fallback branch as the original does. PUC Lua 5.1's xpcall
// passes no arguments to require, which fails there natively too.
func TestCLI_ProtectedRequireOfAMissingModule(t *testing.T) {
	t.Parallel()

	spec := differential(luatest.Project{
		"main.lua": "local ok = pcall(require, \"cjson\")\nprint(\"pcall: \" .. (ok and \"cjson\" or \"fallback\"))\n" +
			"ok = xpcall(require, function() return \"handled\" end, \"cjson\")\n" +
			"print(\"xpcall: \" .. (ok and \"cjson\" or \"fallback\"))\n",
	}, "", testCLIBundler([]string{
		`main\.lua:1: unresolved require "cjson" in protected mode \(pcall or xpcall\): `,
		`main\.lua:3: unresolved require "cjson" in protected mode \(pcall or xpcall\): `,
	}, "--entry", "main.lua", "--strict"))
	spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
		t.Helper()

		require.Equal(t, "pcall: fallback\nxpcall: fallback\n", orig.Stdout, "the original\n%s", orig)
		luatest.AssertSame(t, orig, bundle)
	}

	luatest.Differential(t, spec)
}
