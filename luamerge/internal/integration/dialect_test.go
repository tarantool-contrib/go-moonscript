package integration_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/luatest"
)

// The tests in this file bundle code that only some Lua dialects read: Lua
// 5.1, LuaJIT and Tarantool each accept syntax the others reject. A module is
// bundled whatever it is written in, and wherever the interpreter runs the
// original, the bundle must run the same way.

// dialects says which interpreters read a construct.
type dialects func(interp luatest.Interpreter) bool

// lua51 is PUC Lua 5.1.
func lua51(interp luatest.Interpreter) bool {
	return strings.HasPrefix(interp.Version, "Lua 5.1")
}

// luaJIT is LuaJIT and Tarantool, which is built on it.
func luaJIT(interp luatest.Interpreter) bool {
	return strings.HasPrefix(interp.Version, "LuaJIT ") || strings.HasPrefix(interp.Version, "Tarantool ")
}

// luaJIT52 is LuaJIT built with LUAJIT_ENABLE_LUA52COMPAT, which no
// interpreter the tests find says it is: a construct only it reads is
// compared where the original runs anyway.
func luaJIT52(luatest.Interpreter) bool { return false }

// everyDialect is every interpreter.
func everyDialect(luatest.Interpreter) bool { return true }

// anyDialect is the interpreters any of all accepts.
func anyDialect(all ...dialects) dialects {
	return func(interp luatest.Interpreter) bool {
		for _, accepts := range all {
			if accepts(interp) {
				return true
			}
		}

		return false
	}
}

// checkWhereItRuns returns the check of a differential test whose original
// only some interpreters run: where readers says it runs, the original must
// run through and print stdout, and the bundle must behave the same. Where
// the original fails the check is skipped; where it runs anyway the bundle is
// compared with it all the same.
func checkWhereItRuns(readers dialects, stdout string) func(t *testing.T, orig, bundle luatest.Result) {
	return func(t *testing.T, orig, bundle luatest.Result) {
		t.Helper()

		if !readers(orig.Interp) && orig.ExitCode != 0 {
			first, _, _ := strings.Cut(orig.Stderr, "\n")
			t.Skipf("%s does not read the original: %s", orig.Interp.Name, first)
		}

		if readers(orig.Interp) {
			require.Equal(t, 0, orig.ExitCode, "%s reads the original\n%s", orig.Interp.Name, orig)
			assert.Equal(t, stdout, orig.Stdout, "stdout of the original\n%s", orig)
		}

		luatest.AssertSame(t, orig, bundle)
	}
}

// TestCLI_ModuleNameEscapes: a module name is decoded the way LuaJIT and
// Tarantool decode it, so "\x61x" requires the module ax. The bundler used to
// decode names like Lua 5.1, which takes an unknown escape for the character
// after the backslash, and looked for modules named x61x, u{62}ar and cz   z
// instead.
func TestCLI_ModuleNameEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		literal string
		module  string
		readers dialects
	}{
		{name: "hexadecimal escape", literal: `"\x61x"`, module: "ax", readers: luaJIT},
		{name: "unicode escape", literal: `"\u{62}ar"`, module: "bar", readers: luaJIT},
		{name: "z escape", literal: `"c\z   z"`, module: "cz", readers: luaJIT},
		{name: "z escape over a line break", literal: "\"c\\z\n    z\"", module: "cz", readers: luaJIT},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := differential(luatest.Project{
				"main.lua":         "print((require(" + tt.literal + ")))\n",
				tt.module + ".lua": "return \"" + tt.module + "\"\n",
			}, "", testCLIBundler(nil, "--entry", "main.lua", "--strict"))
			spec.Check = checkWhereItRuns(tt.readers, tt.module+"\n")

			luatest.Differential(t, spec)
		})
	}
}

// dialectCase is a module m.lua written in syntax that only some dialects
// read. main.lua prints what it returns, and the project has a module n.lua
// that returns "n" for m.lua to require.
type dialectCase struct {
	name   string
	module string
	// stdout is what main.lua prints.
	stdout string
	// readers are the interpreters that read m.lua.
	readers dialects
}

// dialectCases are the constructs of every dialect: first each on its own,
// then next to requires, which must be found.
func dialectCases() []dialectCase {
	return slices.Concat([]dialectCase{
		{
			name: "hexadecimal float", module: "return 0x1p4 == 16\n", stdout: "true\n",
			readers: anyDialect(lua51, luaJIT),
		},
		{name: "unsigned 64-bit integer", module: "return tostring(1ULL)\n", stdout: "1ULL\n", readers: luaJIT},
		{name: "64-bit integer", module: "return tostring(0x10LL)\n", stdout: "16LL\n", readers: luaJIT},
		{name: "imaginary number", module: "return tostring(2i)\n", stdout: "0+2i\n", readers: luaJIT},
	}, dialectRequireCases())
}

// dialectRequireCases are modules that require n.lua next to the constructs
// of a dialect.
func dialectRequireCases() []dialectCase {
	return []dialectCase{
		{
			name:    "form feed and vertical tab",
			module:  "local a = \"a\"\f\vlocal n = require(\"n\")\f\n\vreturn a .. n\n",
			stdout:  "an\n",
			readers: everyDialect,
		},
		{
			name:    "z escape over a line break",
			module:  "local s = \"c\\z\n           d\"\nlocal n = require(\"n\")\nreturn s .. n\n",
			stdout:  "cdn\n",
			readers: luaJIT,
		},
		{
			name: "LuaJIT numerals",
			module: "local n = require(\"n\")\n" +
				"local v = 0x10LL + 1ULL + 0b101\n" +
				"local m = require(\"n\")\n" +
				"return tostring(v) .. n .. m\n",
			stdout:  "22ULLnn\n",
			readers: luaJIT,
		},
		{
			name:    "LuaJIT names beyond ASCII",
			module:  "local \xD0\xBF = require(\"n\")\nreturn \xD0\xBF\n",
			stdout:  "n\n",
			readers: luaJIT,
		},
		{
			name: "goto statements",
			module: "local n = require(\"n\")\ngoto skip\nn = nil\n::skip::\n" +
				"for i = 1, 2 do\n  if i == 1 then goto continue end\n" +
				"  n = n .. require(\"n\")\n  ::continue::\nend\n" +
				"return n\n",
			stdout:  "nn\n",
			readers: luaJIT,
		},
		{
			// Some LuaJIT builds read goto as a name too, Tarantool's
			// among them; where the original runs, it is compared.
			name:    "goto as a name",
			module:  "local goto = require(\"n\")\nreturn goto\n",
			stdout:  "n\n",
			readers: lua51,
		},
		{
			name:    "statement after break",
			module:  "local n\nfor i = 1, 2 do\n  n = require(\"n\")\n  break\n  n = nil\nend\nreturn n\n",
			stdout:  "n\n",
			readers: luaJIT52,
		},
		{
			name: "call across a line break",
			module: "local function id(...) return ... end\n" +
				"local f = function() return id end\n" +
				"local n = f()\n" +
				"(require(\"n\"))\n" +
				"return n\n",
			stdout:  "n\n",
			readers: luaJIT52,
		},
		{
			name:    "empty statements",
			module:  ";local n = require(\"n\");;\nreturn n\n",
			stdout:  "n\n",
			readers: luaJIT52,
		},
		{
			name:    "Lua 5.1 numerals before a concatenation",
			module:  "local n = 1e1..require(\"n\")\nreturn 0x10..n\n",
			stdout:  "1610n\n",
			readers: lua51,
		},
		{
			name:    "Lua 5.1 escape LuaJIT does not know",
			module:  "local s = \"a\\.b\"\nlocal n = require(\"n\")\nreturn s .. n\n",
			stdout:  "a.bn\n",
			readers: lua51,
		},
	}
}

// TestCLI_DialectSyntax: a module in the syntax of any dialect bundles, the
// parser reads it and finds its requires, so the CLI has no warning, and on
// each interpreter that runs it the bundle behaves like the original. The
// bundler used to read every module as Lua 5.1 and fail the whole build on
// anything newer.
func TestCLI_DialectSyntax(t *testing.T) {
	t.Parallel()

	for _, tt := range dialectCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := differential(luatest.Project{
				"main.lua": "print((require(\"m\")))\n",
				"m.lua":    tt.module,
				"n.lua":    "return \"n\"\n",
			}, "", testCLIBundler(nil, "--entry", "main.lua", "--strict"))
			spec.Check = checkWhereItRuns(tt.readers, tt.stdout)

			luatest.Differential(t, spec)
		})
	}
}

// TestCLI_TokenScanFallback: a module the parser cannot read, here one with a
// syntax error, does not fail the build. The token scanner finds its
// requires, which are embedded, and the CLI warns with the line the parser
// failed on. With --debug a module compiles when it is first required, as
// natively, so the program that tries the module through pcall runs on in
// the bundle too.
func TestCLI_TokenScanFallback(t *testing.T) {
	t.Parallel()

	project := luatest.Project{
		"main.lua":   "print((pcall(require, \"broken\")))\nprint((require(\"fine\")))\n",
		"broken.lua": "local x = = 1\nreturn require(\"dep\")\n",
		"dep.lua":    "return \"dep\"\n",
		"fine.lua":   "return \"fine\"\n",
	}
	scanned := testCLIBundler(
		[]string{`broken\.lua:1: the parser cannot read the file: unexpected symbol near "="; ` +
			`its requires were found by the token scanner`},
		"--entry", "main.lua", "--strict", "--debug")

	spec := differential(project, "", func(tb testing.TB, dir string) []byte {
		tb.Helper()

		bundle := scanned(tb, dir)
		assert.Contains(tb, string(bundle), `package.preload["dep"]`, "the require the token scanner found")

		return bundle
	})
	spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
		t.Helper()

		luatest.AssertSame(t, orig, bundle)
		assert.Equal(t, "false\nfine\n", orig.Stdout, "stdout of the original\n%s", orig)
	}

	luatest.Differential(t, spec)
}
