package integration_test

import (
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

			spec := cliCase(luatest.Project{
				"main.lua":         "print((require(" + tt.literal + ")))\n",
				tt.module + ".lua": "return \"" + tt.module + "\"\n",
			}, "--strict")
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

// dialectCases are the constructs of every dialect.
func dialectCases() []dialectCase {
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
	}
}

// TestCLI_DialectSyntax: a module in the syntax of any dialect bundles, its
// requires are found, and on each interpreter that runs it the bundle behaves
// like the original. The bundler used to read every module as Lua 5.1 and
// fail the whole build on anything newer.
func TestCLI_DialectSyntax(t *testing.T) {
	t.Parallel()

	for _, tt := range dialectCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := cliCase(luatest.Project{
				"main.lua": "print((require(\"m\")))\n",
				"m.lua":    tt.module,
				"n.lua":    "return \"n\"\n",
			}, "--strict")
			spec.Check = checkWhereItRuns(tt.readers, tt.stdout)

			luatest.Differential(t, spec)
		})
	}
}
