package parse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
)

func TestParseStaticRequire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		expected []parse.RequireInfo
	}{
		{
			name:   "simple require",
			source: `require("foo")`,
			expected: []parse.RequireInfo{
				{
					Name: "foo", Line: 1, Static: true, Literal: parse.Span{Start: 8, End: 13},
					Protected: false, Indirect: "",
				},
			},
		},
		{
			name:   "require with dots",
			source: `require("foo.bar")`,
			expected: []parse.RequireInfo{
				{
					Name: "foo.bar", Line: 1, Static: true, Literal: parse.Span{Start: 8, End: 17},
					Protected: false, Indirect: "",
				},
			},
		},
		{
			name: "multiple requires",
			source: `local a = require("a")
local b = require("b")`,
			expected: []parse.RequireInfo{
				{
					Name: "a", Line: 1, Static: true, Literal: parse.Span{Start: 18, End: 21},
					Protected: false, Indirect: "",
				},
				{
					Name: "b", Line: 2, Static: true, Literal: parse.Span{Start: 41, End: 44},
					Protected: false, Indirect: "",
				},
			},
		},
		{
			name:   "pcall require",
			source: `pcall(require, "module")`,
			expected: []parse.RequireInfo{
				{
					Name: "module", Line: 1, Static: true, Literal: parse.Span{Start: 15, End: 23},
					Protected: true, Indirect: "",
				},
			},
		},
		{
			name:   "dynamic require",
			source: `require(var)`,
			expected: []parse.RequireInfo{
				{Name: "", Line: 1, Static: false, Literal: testNoLiteral(), Protected: false, Indirect: ""},
			},
		},
		{
			name: "require in function",
			source: `function f()
  require("inner")
end`,
			expected: []parse.RequireInfo{
				{
					Name: "inner", Line: 2, Static: true, Literal: parse.Span{Start: 23, End: 30},
					Protected: false, Indirect: "",
				},
			},
		},
		{
			name:     "no requires",
			source:   `print("hello")`,
			expected: []parse.RequireInfo{},
		},
	}

	parser := parse.New()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			found, err := parser.Parse([]byte(tt.source), "test.lua")
			require.NoError(t, err, "Parse() error")
			require.Nil(t, found.Fallback, "the parser reads the source")
			assert.Equal(t, tt.expected, found.Requires)
		})
	}
}

// testParse parses source and returns what it found, without the positions
// of the literals (see testWithoutLiterals); the parser, not the token
// scanner, must read the source, and the literal of each static call must be
// a string literal of the source whose value is its module name.
func testParse(t *testing.T, source string) []parse.RequireInfo {
	t.Helper()

	found, err := parse.New().Parse([]byte(source), "test.lua")
	require.NoError(t, err, "Parse(%q)", source)
	require.Nil(t, found.Fallback, "the parser reads %q", source)

	for _, call := range found.Requires {
		if call.Static {
			value, ok := parse.LiteralValueForTest([]byte(source[call.Literal.Start:call.Literal.End]))
			assert.True(t, ok && value == call.Name, "%q: the literal of %+v", source, call)
		}
	}

	return testWithoutLiterals(found.Requires)
}

// testWithoutLiterals returns requires with the positions of their literals
// cleared. Most tests are about which requires are found, on which lines;
// TestParse_ModuleNameLiterals is about where their literals are.
func testWithoutLiterals(requires []parse.RequireInfo) []parse.RequireInfo {
	cleared := make([]parse.RequireInfo, 0, len(requires))

	for _, info := range requires {
		info.Literal = testNoLiteral()
		cleared = append(cleared, info)
	}

	return cleared
}

// testNoLiteral is the Literal of a use of require that names no module by a
// literal, or whose literal is left out.
func testNoLiteral() parse.Span {
	return parse.Span{Start: 0, End: 0}
}

// testStatic is the RequireInfo of a require call with the literal name on
// line, without the position of the literal.
func testStatic(name string, line int) parse.RequireInfo {
	return parse.RequireInfo{
		Name: name, Line: line, Static: true, Literal: testNoLiteral(), Protected: false, Indirect: "",
	}
}

// testDynamic is the RequireInfo of a require call on line whose module name
// is not a literal.
func testDynamic(line int) parse.RequireInfo {
	return parse.RequireInfo{
		Name: "", Line: line, Static: false, Literal: testNoLiteral(), Protected: false, Indirect: "",
	}
}

// testProtected is info as a call of require in protected mode, through
// pcall or xpcall.
func testProtected(info parse.RequireInfo) parse.RequireInfo {
	info.Protected = true

	return info
}

// testIndirect is the RequireInfo of a use of require on line that is not a
// call; use says how require is used.
func testIndirect(use string, line int) parse.RequireInfo {
	return parse.RequireInfo{
		Name: "", Line: line, Static: false, Literal: testNoLiteral(), Protected: false, Indirect: use,
	}
}

// TestParse_RequireAsReceiver: a require whose result is the receiver of a
// method call, `require("x"):m()`, sits in the call's Receiver rather than in
// its Func or Args. A walker that does not visit the receiver leaves the module
// out of the bundle without a warning, even with --strict.
func TestParse_RequireAsReceiver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		{
			name:   "constructor called on the module",
			source: "local c = require(\"counter\"):new(5)\nprint(\"counter:\", c.n)\n",
			want:   []parse.RequireInfo{testStatic("counter", 1)},
		},
		{
			name:   "call result as receiver",
			source: `require("x"):new()`,
			want:   []parse.RequireInfo{testStatic("x", 1)},
		},
		{
			name:   "parenthesized string call as receiver",
			source: `print((require "x"):upper())`,
			want:   []parse.RequireInfo{testStatic("x", 1)},
		},
		{
			name:   "field of the module as receiver",
			source: `require("x").field:m()`,
			want:   []parse.RequireInfo{testStatic("x", 1)},
		},
		{
			name:   "requires inside a method chain",
			source: `app:use(require "a"):use(require "b"):listen()`,
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 1)},
		},
		{
			name:   "method chain over several lines",
			source: "app\n  :use(require \"a\")\n  :use(require \"b\")\n  :listen()\n",
			want:   []parse.RequireInfo{testStatic("a", 2), testStatic("b", 3)},
		},
		{
			name:   "module that starts a method chain",
			source: `require("app"):use(require "a"):listen()`,
			want:   []parse.RequireInfo{testStatic("app", 1), testStatic("a", 1)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testParse(t, tt.source))
		})
	}
}

// TestParse_IndirectRequire: require reached other than by a plain call of the
// name require must not be dropped silently. The forms that name their module
// literally are resolved (`_G.require("x")`, `xpcall(require, h, "x")`), a call
// with a computed name is dynamic whichever form it takes, and every other use
// of require is reported as indirect, except binding it to a name the walker
// already finds calls by, as in the common `local require = require`.
func TestParse_IndirectRequire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		// Forms whose module name is a literal: resolved statically.
		{
			name:   "_G.require",
			source: `print((_G.require("m2")))`,
			want:   []parse.RequireInfo{testStatic("m2", 1)},
		},
		{
			name:   "_G indexed with a string",
			source: `_G["require"]("x")`,
			want:   []parse.RequireInfo{testStatic("x", 1)},
		},
		{
			name:   "_ENV.require",
			source: `_ENV.require("x")`,
			want:   []parse.RequireInfo{testStatic("x", 1)},
		},
		{
			name:   "xpcall",
			source: `print(select(2, xpcall(require, debug.traceback, "m3")))`,
			want:   []parse.RequireInfo{testProtected(testStatic("m3", 1))},
		},
		{
			name:   "xpcall with extra arguments",
			source: `xpcall(require, handler, "x", "ignored")`,
			want:   []parse.RequireInfo{testProtected(testStatic("x", 1))},
		},
		{
			name:   "pcall of _G.require",
			source: `pcall(_G.require, "x")`,
			want:   []parse.RequireInfo{testProtected(testStatic("x", 1))},
		},
		{
			name:   "xpcall of _G.require",
			source: `xpcall(_G.require, handler, "x")`,
			want:   []parse.RequireInfo{testProtected(testStatic("x", 1))},
		},
		{
			name:   "extra arguments are ignored",
			source: `require("x", "ignored")`,
			want:   []parse.RequireInfo{testStatic("x", 1)},
		},
		{
			name:   "local require = require",
			source: "local require = require\nlocal m = require(\"x\")\n",
			want:   []parse.RequireInfo{testStatic("x", 2)},
		},
		{
			name:   "local require = _G.require",
			source: "local require = _G.require\nrequire(\"x\")\n",
			want:   []parse.RequireInfo{testStatic("x", 2)},
		},
		{
			name:   "require cached among other globals",
			source: "local pcall, require = pcall, require\npcall(require, \"x\")\n",
			want:   []parse.RequireInfo{testProtected(testStatic("x", 2))},
		},
		{
			name:   "global require assigned to itself",
			source: "require = require\nrequire(\"x\")\n",
			want:   []parse.RequireInfo{testStatic("x", 2)},
		},
		{
			name:   "require stored back into _G",
			source: "_G.require = require\n_G.require(\"x\")\n",
			want:   []parse.RequireInfo{testStatic("x", 2)},
		},

		// Calls whose module name is computed: dynamic, whichever form.
		{
			name:   "pcall with a variable",
			source: "local name = \"m4\"\nprint(select(2, pcall(require, name)))\n",
			want:   []parse.RequireInfo{testProtected(testDynamic(2))},
		},
		{
			name:   "xpcall with a variable",
			source: `xpcall(require, handler, name)`,
			want:   []parse.RequireInfo{testProtected(testDynamic(1))},
		},
		{
			name:   "_G.require with a variable",
			source: `_G.require(name)`,
			want:   []parse.RequireInfo{testDynamic(1)},
		},
		{
			name:   "pcall without a module name",
			source: `pcall(require)`,
			want:   []parse.RequireInfo{testProtected(testDynamic(1))},
		},
		{
			name:   "xpcall without a module name",
			source: `xpcall(require, handler)`,
			want:   []parse.RequireInfo{testProtected(testDynamic(1))},
		},
		{
			name:   "xpcall without a handler",
			source: `xpcall(require)`,
			want:   []parse.RequireInfo{testProtected(testDynamic(1))},
		},

		// Uses of require that are not calls: indirect.
		{
			name:   "alias",
			source: `local r = require; print((r("m1")))`,
			want:   []parse.RequireInfo{testIndirect(`require aliased as local "r"`, 1)},
		},
		{
			name:   "alias of _G.require",
			source: `local r = _G.require`,
			want:   []parse.RequireInfo{testIndirect(`_G.require aliased as local "r"`, 1)},
		},
		{
			name:   "alias among several locals",
			source: `local a, r = 1, require`,
			want:   []parse.RequireInfo{testIndirect(`require aliased as local "r"`, 1)},
		},
		{
			name:   "assigned to a name",
			source: "local r = nil\nr = require\n",
			want:   []parse.RequireInfo{testIndirect(`require assigned to "r"`, 2)},
		},
		{
			name:   "assigned to a table field",
			source: `M.load = require`,
			want:   []parse.RequireInfo{testIndirect(`require assigned to a table field`, 1)},
		},
		{
			name:   "passed as an argument",
			source: `setup(require)`,
			want:   []parse.RequireInfo{testIndirect(`require used as a value`, 1)},
		},
		{
			name:   "stored in a table constructor",
			source: `local env = { require = require }`,
			want:   []parse.RequireInfo{testIndirect(`require used as a value`, 1)},
		},
		{
			name:   "returned",
			source: `return require`,
			want:   []parse.RequireInfo{testIndirect(`require used as a value`, 1)},
		},
		{
			name:   "operand of or",
			source: `local r = require or dofile`,
			want:   []parse.RequireInfo{testIndirect(`require used as a value`, 1)},
		},
		{
			name:   "_G.require passed as an argument",
			source: `setup(_G.require)`,
			want:   []parse.RequireInfo{testIndirect(`_G.require used as a value`, 1)},
		},
		{
			name:   "line of the use itself",
			source: "setup(\n  require\n)\n",
			want:   []parse.RequireInfo{testIndirect(`require used as a value`, 2)},
		},
		{
			name:   "surplus value of an assignment",
			source: `a = 1, require`,
			want:   []parse.RequireInfo{testIndirect(`require used as a value`, 1)},
		},
		{
			name:   "surplus value of a local declaration",
			source: `local a = 1, require`,
			want:   []parse.RequireInfo{testIndirect(`require used as a value`, 1)},
		},

		// Not the global require at all.
		{
			name:   "method named require",
			source: `loader:require("x")`,
			want:   []parse.RequireInfo{},
		},
		{
			name:   "field named require",
			source: `M.require("x")`,
			want:   []parse.RequireInfo{},
		},
		{
			name:   "field named require of a nested table",
			source: `M.loader.require("x")`,
			want:   []parse.RequireInfo{},
		},
		{
			name:   "pcall and xpcall without arguments",
			source: "pcall()\nxpcall()\n",
			want:   []parse.RequireInfo{},
		},
		{
			name:   "other fields of _G and _ENV",
			source: "_G.print(\"x\")\nlocal p = _ENV.print\nsetup(_G.dofile, _G[\"loadfile\"])\n",
			want:   []parse.RequireInfo{},
		},
		{
			name:   "functions named require",
			source: "function M.require(name) end\nlocal function require(name) end\n",
			want:   []parse.RequireInfo{},
		},
		{
			name:   "global require rebound",
			source: `require = function(name) end`,
			want:   []parse.RequireInfo{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testParse(t, tt.source))
		})
	}
}

// TestParse_ProtectedRequire: a require called in protected mode by pcall or
// xpcall is marked, since the program survives the module's absence. Only
// the forms in which pcall or xpcall itself calls require count: a require
// called before pcall runs, or inside a function pcall calls, is not marked.
func TestParse_ProtectedRequire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		{
			name:   "pcall",
			source: `local ok, cjson = pcall(require, "cjson")`,
			want:   []parse.RequireInfo{testProtected(testStatic("cjson", 1))},
		},
		{
			name:   "xpcall",
			source: `local ok, cjson = xpcall(require, debug.traceback, "cjson")`,
			want:   []parse.RequireInfo{testProtected(testStatic("cjson", 1))},
		},
		{
			name:   "pcall in a condition",
			source: "if not pcall(require, \"cjson\") then\n  print(\"fallback\")\nend\n",
			want:   []parse.RequireInfo{testProtected(testStatic("cjson", 1))},
		},
		{
			name:   "require called before pcall",
			source: `pcall(require("cjson"))`,
			want:   []parse.RequireInfo{testStatic("cjson", 1)},
		},
		{
			name:   "require inside the function pcall calls",
			source: `pcall(function() return require("cjson") end)`,
			want:   []parse.RequireInfo{testStatic("cjson", 1)},
		},
		{
			name:   "pcall of another function",
			source: `pcall(dofile, "cjson")`,
			want:   []parse.RequireInfo{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testParse(t, tt.source))
		})
	}
}
