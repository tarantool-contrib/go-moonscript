package integration_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/luatest"
)

// The tests in this file bundle code that only some Lua dialects read: Lua
// 5.2 to 5.5, LuaJIT and Tarantool each accept syntax the others reject. A
// module is bundled whatever it is written in, and wherever the interpreter
// runs the original, the bundle must run the same way.

// dialects says which interpreters read a construct.
type dialects func(interp luatest.Interpreter) bool

// pucLuaMinor is the minor version of PUC Lua, e.g. 4 for "Lua 5.4", and false
// for any other interpreter.
func pucLuaMinor(interp luatest.Interpreter) (int, bool) {
	minor, found := strings.CutPrefix(interp.Version, "Lua 5.")
	if !found {
		return 0, false
	}

	n, err := strconv.Atoi(minor)

	return n, err == nil
}

// pucLuaFrom is PUC Lua 5.minor and later.
func pucLuaFrom(minor int) dialects {
	return func(interp luatest.Interpreter) bool {
		n, ok := pucLuaMinor(interp)

		return ok && n >= minor
	}
}

// pucLuaOnly is PUC Lua 5.minor for each of minors.
func pucLuaOnly(minors ...int) dialects {
	return func(interp luatest.Interpreter) bool {
		n, ok := pucLuaMinor(interp)

		return ok && slices.Contains(minors, n)
	}
}

// luaJITFrom is LuaJIT 2.1 from the build stamped stamp on: its version is
// "LuaJIT 2.1.<stamp>". Tarantool is not.
func luaJITFrom(stamp int) dialects {
	return func(interp luatest.Interpreter) bool {
		build, found := strings.CutPrefix(interp.Version, "LuaJIT 2.1.")
		if !found {
			return false
		}

		n, err := strconv.Atoi(build)

		return err == nil && n >= stamp
	}
}

// luaJIT is LuaJIT and Tarantool, which is built on it.
func luaJIT(interp luatest.Interpreter) bool {
	return strings.HasPrefix(interp.Version, "LuaJIT ") || strings.HasPrefix(interp.Version, "Tarantool ")
}

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

// TestCLI_ModuleNameEscapes: a module name is decoded the way the interpreters
// that know its escapes decode it, so "\x61x" requires the module ax. The
// bundler used to decode names like Lua 5.1, which takes an unknown escape
// for the character after the backslash, and looked for modules named x61x,
// u{62}ar and cz   z instead.
func TestCLI_ModuleNameEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		literal string
		module  string
		readers dialects
	}{
		{name: "hexadecimal escape", literal: `"\x61x"`, module: "ax", readers: anyDialect(pucLuaFrom(2), luaJIT)},
		{name: "unicode escape", literal: `"\u{62}ar"`, module: "bar", readers: anyDialect(pucLuaFrom(3), luaJIT)},
		{name: "z escape", literal: `"c\z   z"`, module: "cz", readers: anyDialect(pucLuaFrom(2), luaJIT)},
		{
			name: "z escape over a line break", literal: "\"c\\z\n    z\"", module: "cz",
			readers: anyDialect(pucLuaFrom(2), luaJIT),
		},
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
		{name: "floor division", module: "return 7 // 2\n", stdout: "3\n", readers: pucLuaFrom(3)},
		{name: "bitwise operators", module: "return 5 & 3 | 8\n", stdout: "9\n", readers: pucLuaFrom(3)},
		{name: "shift", module: "return 1 << 4\n", stdout: "16\n", readers: pucLuaFrom(3)},
		{name: "const", module: "local x <const> = 42 return x\n", stdout: "42\n", readers: pucLuaFrom(4)},
		{
			name: "hexadecimal float", module: "return 0x1p4 == 16\n", stdout: "true\n",
			readers: anyDialect(pucLuaFrom(2), luaJIT),
		},
		{name: "global declaration", module: "global print; return \"g\"\n", stdout: "g\n", readers: pucLuaFrom(5)},
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
			readers: anyDialect(pucLuaFrom(2), luaJIT),
		},
		{
			name: "Lua 5.3 operators",
			module: "local n = require(\"n\")\n" +
				"local v = 7 // 2 + (5 & 3 | 8) + (1 << 4) + (~0 ~ 1) + (256 >> 4)\n" +
				"local m = require(\"n\")\n" +
				"return v .. n .. m\n",
			stdout:  "42nn\n",
			readers: pucLuaFrom(3),
		},
		{
			name: "Lua 5.4 attributes",
			module: "local n <const> = require(\"n\")\n" +
				"local c <close> = nil\n" +
				"local m <const> = require(\"n\")\n" +
				"return n .. m\n",
			stdout:  "nn\n",
			readers: pucLuaFrom(4),
		},
		{
			name: "Lua 5.5 declarations",
			module: "local <const> a, b = require(\"n\"), 1\n" +
				"global <const> print, require, tostring\n" +
				"local m = require(\"n\")\n" +
				"global function twice(s) return s .. s end\n" +
				"return twice(a) .. m .. tostring(b)\n",
			stdout:  "nnn1\n",
			readers: pucLuaFrom(5),
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
			// Some LuaJIT builds read goto as a name too, Tarantool's
			// among them; where the original runs, it is compared.
			name:    "goto as a name",
			module:  "local goto = require(\"n\")\nreturn goto\n",
			stdout:  "n\n",
			readers: pucLuaOnly(1),
		},
		{
			name:    "Lua 5.2 statement after break",
			module:  "local n\nfor i = 1, 2 do\n  n = require(\"n\")\n  break\n  n = nil\nend\nreturn n\n",
			stdout:  "n\n",
			readers: pucLuaFrom(2),
		},
		{
			name: "Lua 5.2 call across a line break",
			module: "local function id(...) return ... end\n" +
				"local f = function() return id end\n" +
				"local n = f()\n" +
				"(require(\"n\"))\n" +
				"return n\n",
			stdout:  "n\n",
			readers: pucLuaFrom(2),
		},
		{
			name:    "Lua 5.1 numerals before a concatenation",
			module:  "local n = 1e1..require(\"n\")\nreturn 0x10..n\n",
			stdout:  "1610n\n",
			readers: pucLuaOnly(1),
		},
		{
			name:    "Lua 5.2 numeral before a keyword",
			module:  "local x, n = 3, nil\nif x == 3then n = require(\"n\") end\nreturn n\n",
			stdout:  "n\n",
			readers: pucLuaOnly(2, 3),
		},
		{
			// Read since the LuaJIT 2.1 builds of 2026, whose jit.* modules
			// use it; older builds and Tarantool reject it.
			name: "LuaJIT extensions",
			module: "local n = require(\"n\")\n" +
				"local a = 1 != 2 and !false\n" +
				"local b = (a && true || false) ? \"yes\" : \"no\"\n" +
				"local c = nil ?? require(\"n\")\n" +
				"local s = n\n" +
				"s ..= c\n" +
				"local x = 5\n" +
				"x += 1 x -= 2 x *= 3 x %= 7 x &= 7 x |= 8 x <<= 1 x >>= 1 x ~= 1\n" +
				"for i = 1, 3 do\n  if i == 2 then continue end\n  x += i\nend\n" +
				"local y = -16 ~>> 2\n" +
				"y ~>>= 1\n" +
				"return b .. s .. x .. y\n",
			stdout:  "yesnn16-2\n",
			readers: luaJITFrom(1788856981),
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
		[]string{`broken\.lua:1: the parser cannot read the file: syntax error near "="; ` +
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
