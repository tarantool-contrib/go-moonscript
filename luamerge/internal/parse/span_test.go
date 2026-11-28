package parse_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
)

// testLiteral is where a string literal names the module of a static call:
// the n-th occurrence of text in the source, counted from 0.
type testLiteral struct {
	name string
	text string
	n    int
}

// testLiterals returns the module name and the literal text of every static
// call of requires, found in source.
func testLiterals(t *testing.T, source string, requires []parse.RequireInfo) [][2]string {
	t.Helper()

	literals := [][2]string{}

	for _, call := range requires {
		if !call.Static {
			assert.Equal(t, testNoLiteral(), call.Literal, "a call that is not static has no literal: %+v", call)

			continue
		}

		require.True(t, 0 <= call.Literal.Start && call.Literal.Start < call.Literal.End &&
			call.Literal.End <= len(source), "the literal of %+v lies in the source", call)
		literals = append(literals, [2]string{call.Name, source[call.Literal.Start:call.Literal.End]})
	}

	return literals
}

// testSpans returns the spans of want in source, in order.
func testSpans(t *testing.T, source string, want []testLiteral) []parse.Span {
	t.Helper()

	spans := make([]parse.Span, 0, len(want))

	for _, lit := range want {
		start := 0

		for range lit.n + 1 {
			i := strings.Index(source[start:], lit.text)
			require.GreaterOrEqual(t, i, 0, "occurrence %d of %q in %q", lit.n, lit.text, source)

			start += i + len(lit.text)
		}

		spans = append(spans, parse.Span{Start: start - len(lit.text), End: start})
	}

	return spans
}

// testStaticSpans returns the spans of the literals of the static calls of
// requires.
func testStaticSpans(requires []parse.RequireInfo) []parse.Span {
	spans := []parse.Span{}

	for _, call := range requires {
		if call.Static {
			spans = append(spans, call.Literal)
		}
	}

	return spans
}

// TestParse_ModuleNameLiterals: for every static call of require, the parser
// and the token scanner report where the string literal that names its
// module lies, the literal require gets first, in every form of the call:
// the bundler renames the module there. A string of the same text elsewhere
// on the line is not taken for it.
func TestParse_ModuleNameLiterals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []testLiteral
	}{
		{name: "parentheses", source: `local m = require("x")`, want: []testLiteral{{"x", `"x"`, 0}}},
		{name: "a string", source: `require "x"`, want: []testLiteral{{"x", `"x"`, 0}}},
		{name: "single quotes", source: `require 'x'`, want: []testLiteral{{"x", `'x'`, 0}}},
		{name: "long bracket", source: `require[[x]]`, want: []testLiteral{{"x", `[[x]]`, 0}}},
		{
			name:   "leveled long bracket over a line break",
			source: "local m = require [==[\nx]==]\nreturn m\n",
			want:   []testLiteral{{"x", "[==[\nx]==]", 0}},
		},
		{name: "in parentheses", source: `require(("x"))`, want: []testLiteral{{"x", `"x"`, 0}}},
		{name: "more arguments", source: `require("x", "x")`, want: []testLiteral{{"x", `"x"`, 0}}},
		{name: "escapes", source: `require("\x61x")`, want: []testLiteral{{"ax", `"\x61x"`, 0}}},
		{name: "_G.require", source: `_G.require("x")`, want: []testLiteral{{"x", `"x"`, 0}}},
		{name: "_ENV.require", source: `_ENV.require "x"`, want: []testLiteral{{"x", `"x"`, 0}}},
		{name: "_G[\"require\"]", source: `_G["require"]("x")`, want: []testLiteral{{"x", `"x"`, 0}}},
		{name: "pcall", source: `local ok, m = pcall(require, "x")`, want: []testLiteral{{"x", `"x"`, 0}}},
		{
			name:   "xpcall",
			source: `xpcall(require, function(e) return "x" end, "x")`,
			want:   []testLiteral{{"x", `"x"`, 1}},
		},
		{name: "receiver", source: `local c = require("x"):new("x")`, want: []testLiteral{{"x", `"x"`, 0}}},
		{
			name:   "method chain",
			source: `app:use(require "a"):use(require "b")`,
			want:   []testLiteral{{"a", `"a"`, 0}, {"b", `"b"`, 0}},
		},
		{
			name:   "the same name twice on a line",
			source: `local a, b = require("x"), require("x")`,
			want:   []testLiteral{{"x", `"x"`, 0}, {"x", `"x"`, 1}},
		},
		{
			name:   "the name in another string first",
			source: "print(\"x\", require(\"x\"))\n",
			want:   []testLiteral{{"x", `"x"`, 1}},
		},
		{
			name:   "after a comment and a header",
			source: "#!/usr/bin/env lua\n-- require(\"x\")\nlocal m = require --[[ \"x\" ]] (\"x\")\n",
			want:   []testLiteral{{"x", `"x"`, 2}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spans := testSpans(t, tt.source, tt.want)
			literals := [][2]string{}

			for _, lit := range tt.want {
				literals = append(literals, [2]string{lit.name, lit.text})
			}

			found, err := parse.New().Parse([]byte(tt.source), "test.lua")
			require.NoError(t, err)
			require.Nil(t, found.Fallback, "the parser reads %q", tt.source)
			assert.Equal(t, literals, testLiterals(t, tt.source, found.Requires), "parser")
			assert.Equal(t, spans, testStaticSpans(found.Requires), "parser")

			scanned, err := parse.ScanRequiresForTest([]byte(tt.source))
			require.NoError(t, err)
			assert.Equal(t, literals, testLiterals(t, tt.source, scanned), "token scanner")
			assert.Equal(t, spans, testStaticSpans(scanned), "token scanner")
		})
	}
}

// TestParse_ModuleNameLiteralsInScope: a call of a local named require that
// is a function of its own is no require, so it has no literal to rename;
// the parser reports the literal of the global require on the same line
// only. The token scanner knows no scopes and reports both.
func TestParse_ModuleNameLiteralsInScope(t *testing.T) {
	t.Parallel()

	source := "local function require(n) return n end print(require(\"x\"), _G.require(\"x\"))\n"

	found, err := parse.New().Parse([]byte(source), "test.lua")
	require.NoError(t, err)
	require.Nil(t, found.Fallback)
	assert.Equal(t, testSpans(t, source, []testLiteral{{"x", `"x"`, 1}}), testStaticSpans(found.Requires))

	scanned, err := parse.ScanRequiresForTest([]byte(source))
	require.NoError(t, err)
	assert.Equal(t, testSpans(t, source, []testLiteral{{"x", `"x"`, 0}, {"x", `"x"`, 1}}), testStaticSpans(scanned))
}

// TestParse_ModuleNameLiteralsOfAScannedFile: in a file the parser cannot
// read, the literals are where the token scanner finds them.
func TestParse_ModuleNameLiteralsOfAScannedFile(t *testing.T) {
	t.Parallel()

	source := "local x = = 1\nlocal m = require(\"m\")\nreturn require [[n]]\n"

	found, err := parse.New().Parse([]byte(source), "test.lua")
	require.NoError(t, err)
	require.NotNil(t, found.Fallback, "the parser cannot read the file")
	assert.Equal(t, [][2]string{{"m", `"m"`}, {"n", `[[n]]`}}, testLiterals(t, source, found.Requires))
}
