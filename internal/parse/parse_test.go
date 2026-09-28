package parse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/parse"
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
				{Name: "foo", Line: 1, Static: true},
			},
		},
		{
			name:   "require with dots",
			source: `require("foo.bar")`,
			expected: []parse.RequireInfo{
				{Name: "foo.bar", Line: 1, Static: true},
			},
		},
		{
			name: "multiple requires",
			source: `local a = require("a")
local b = require("b")`,
			expected: []parse.RequireInfo{
				{Name: "a", Line: 1, Static: true},
				{Name: "b", Line: 2, Static: true},
			},
		},
		{
			name:   "pcall require",
			source: `pcall(require, "module")`,
			expected: []parse.RequireInfo{
				{Name: "module", Line: 1, Static: true},
			},
		},
		{
			name:   "dynamic require",
			source: `require(var)`,
			expected: []parse.RequireInfo{
				{Name: "", Line: 1, Static: false},
			},
		},
		{
			name: "require in function",
			source: `function f()
  require("inner")
end`,
			expected: []parse.RequireInfo{
				{Name: "inner", Line: 2, Static: true},
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

			got, err := parser.Parse([]byte(tt.source), "test.lua")
			require.NoError(t, err, "Parse() error")
			require.Len(t, got, len(tt.expected), "Parse() requires count")

			for i := range got {
				assert.Equal(t, tt.expected[i].Name, got[i].Name, "require %d: Name", i)
				assert.Equal(t, tt.expected[i].Line, got[i].Line, "require %d: Line", i)
				assert.Equal(t, tt.expected[i].Static, got[i].Static, "require %d: Static", i)
			}
		})
	}
}

// testParse parses source and returns what it found; the source must parse.
func testParse(t *testing.T, source string) []parse.RequireInfo {
	t.Helper()

	got, err := parse.New().Parse([]byte(source), "test.lua")
	require.NoError(t, err, "Parse(%q)", source)

	return got
}

// testStatic is the RequireInfo of a require call with the literal name on
// line.
func testStatic(name string, line int) parse.RequireInfo {
	return parse.RequireInfo{Name: name, Line: line, Static: true}
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
