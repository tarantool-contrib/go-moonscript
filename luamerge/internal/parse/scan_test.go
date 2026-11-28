package parse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
)

// TestParse_TokenScanFallback: a file the parser cannot read even written the
// Lua 5.1 way, typically one with a syntax error, does not fail the build: the
// token scanner finds its requires, and the result says where and why the
// parser failed, so the bundler can say so.
func TestParse_TokenScanFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		want     []parse.RequireInfo
		fallback parse.Fallback
	}{
		{
			name:     "syntax error before the requires",
			source:   "local x = = 1\nlocal a = require(\"a\")\nlocal b = require \"b\"\n",
			want:     []parse.RequireInfo{testStatic("a", 2), testStatic("b", 3)},
			fallback: parse.Fallback{Line: 1, Reason: `unexpected symbol near "="`},
		},
		{
			name:     "a character that starts no token",
			source:   "local a = require(\"a\") $\n",
			want:     []parse.RequireInfo{testStatic("a", 1)},
			fallback: parse.Fallback{Line: 1, Reason: `unexpected symbol near "$"`},
		},
		{
			name:   "end of the file inside a function",
			source: "local f = function()\n  return require(\"a\")\n\n-- the end\n",
			want:   []parse.RequireInfo{testStatic("a", 2)},
			fallback: parse.Fallback{
				Line:   2,
				Reason: "'end' expected (to close 'function' at line 1) at the end of the file",
			},
		},
		{
			// The parser reads string literals marked with their token; the
			// reason names the string as it is in the source.
			name:     "syntax error at a string",
			source:   "local a = require(\"a\")\nlocal x = 1 \"b\"\n",
			want:     []parse.RequireInfo{testStatic("a", 1)},
			fallback: parse.Fallback{Line: 2, Reason: `unexpected symbol near "b"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			found, err := parse.New().Parse([]byte(tt.source), "test.lua")
			require.NoError(t, err, "a file the parser cannot read is scanned")
			require.NotNil(t, found.Fallback, "the parser does not read the file")
			assert.Equal(t, tt.fallback, *found.Fallback)
			assert.Equal(t, tt.want, testWithoutLiterals(found.Requires))
		})
	}
}

// TestParse_LexicalError: a file that no dialect reads, as it has a string
// without its end, fails the build with the line of the string.
func TestParse_LexicalError(t *testing.T) {
	t.Parallel()

	_, err := parse.New().Parse([]byte("local a = require(\"a\")\nlocal s = \"abc\n"), "test.lua")
	require.ErrorIs(t, err, lex.ErrUnfinishedString)
	assert.Contains(t, err.Error(), "line 2")
}

// testScan returns what the token scanner finds in source, without the
// positions of the literals (see testWithoutLiterals).
func testScan(t *testing.T, source string) []parse.RequireInfo {
	t.Helper()

	found, err := parse.ScanRequiresForTest([]byte(source))
	require.NoError(t, err, "scan %q", source)

	return testWithoutLiterals(found)
}

// TestScanRequires_Calls: the token scanner finds the calls of require the
// parser finds, in the same forms, static where require gets a string literal
// first and dynamic otherwise.
func TestScanRequires_Calls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		{
			name:   "literal forms",
			source: "require(\"a\")\nrequire 'b'\nrequire [[c]]\nrequire((\"d\"))\nrequire(\"e\", \"extra\")\n",
			want: []parse.RequireInfo{
				testStatic("a", 1), testStatic("b", 2), testStatic("c", 3), testStatic("d", 4), testStatic("e", 5),
			},
		},
		{
			name:   "escapes decoded",
			source: `require("\x61x")`,
			want:   []parse.RequireInfo{testStatic("ax", 1)},
		},
		{
			name:   "computed names",
			source: "require(name)\nrequire(\"f\" .. x)\nrequire((\"g\") .. x)\nrequire {}\nrequire()\n",
			want: []parse.RequireInfo{
				testDynamic(1), testDynamic(2), testDynamic(3), testDynamic(4), testDynamic(5),
			},
		},
		{
			name:   "through _G and _ENV",
			source: "_G.require(\"a\")\n_ENV.require(\"b\")\n_G[\"require\"](\"c\")\n_G['require'] \"d\"\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2), testStatic("c", 3), testStatic("d", 4)},
		},
		{
			name: "pcall and xpcall",
			source: "pcall(require, \"a\")\npcall(_G.require, \"b\")\n" +
				"xpcall(require, handler, \"c\")\n" +
				"xpcall(require, function(e) return e, f(1, 2) end, \"d\")\n" +
				"pcall(require, name)\npcall(require)\nxpcall(require, h)\nxpcall(require)\n",
			want: []parse.RequireInfo{
				testProtected(testStatic("a", 1)), testProtected(testStatic("b", 2)),
				testProtected(testStatic("c", 3)), testProtected(testStatic("d", 4)),
				testProtected(testDynamic(5)), testProtected(testDynamic(6)),
				testProtected(testDynamic(7)), testProtected(testDynamic(8)),
			},
		},
		{
			name:   "method call on the module",
			source: "local c = require(\"counter\"):new(5)\napp:use(require \"a\"):use(require \"b\")\n",
			want:   []parse.RequireInfo{testStatic("counter", 1), testStatic("a", 2), testStatic("b", 2)},
		},
		{
			name: "not the global require",
			source: "loader:require(\"a\")\nM.require(\"b\")\nM.loader.require(\"c\")\n" +
				"function M.require(name) end\nt._G.require(\"d\")\npcall(dofile, \"e\")\n",
			want: []parse.RequireInfo{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testScan(t, tt.source))
		})
	}
}

// TestScanRequires_OtherUses: every other use of the name require is
// reported as indirect, as the calls through it cannot be seen, except the
// names being declared or assigned to and `require = require`.
func TestScanRequires_OtherUses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		{
			name:   "alias",
			source: "local r = require\nr(\"m1\")\n",
			want:   []parse.RequireInfo{testIndirect("require used as a value", 1)},
		},
		{
			name:   "argument, table field and return value",
			source: "setup(require)\nlocal env = { require = require }\nreturn _G.require\n",
			want: []parse.RequireInfo{
				testIndirect("require used as a value", 1),
				testIndirect("require used as a value", 2),
				testIndirect("_G.require used as a value", 3),
			},
		},
		{
			name: "declared or assigned to",
			source: "local require = require\n_G.require = require\n" +
				"local function require(name) end\nrequire = require\nfunction require(name) end\n" +
				"local a, require <const> = 1, f\nfor k, require in pairs(t) do end\n" +
				"local f = function(a, require, ...) end\nglobal require\n",
			want: []parse.RequireInfo{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testScan(t, tt.source))
		})
	}
}

// TestScanRequires_EveryPosition: the token scanner finds require("x") on its
// line wherever the parser finds it.
func TestScanRequires_EveryPosition(t *testing.T) {
	t.Parallel()

	for _, p := range testPositions() {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, []parse.RequireInfo{testStatic("x", p.line)}, testScan(t, p.source))
		})
	}
}
