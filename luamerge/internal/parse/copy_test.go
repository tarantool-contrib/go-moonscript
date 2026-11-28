package parse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
)

// TestParse_CopyLiteralsAndLines: in a source that the parser reads from its
// copy, where tokens before a literal are written longer or shorter than in
// the source, every literal of a module name is still where it is in the
// source, and every call on its line.
func TestParse_CopyLiteralsAndLines(t *testing.T) {
	t.Parallel()

	type call struct {
		name, literal string
		line          int
		protected     bool
	}

	tests := []struct {
		name   string
		source string
		want   []call
	}{
		{
			name:   "attributes and operators before the literal",
			source: `local x <const>, y <close> = ~1 // 2 << 3, !x && y; local a = require("a") ~ require 'b'`,
			want:   []call{{"a", `"a"`, 1, false}, {"b", `'b'`, 1, false}},
		},
		{
			name:   "a global declaration before the literal",
			source: `global a, b = require [[a]], 1 require"b"`,
			want:   []call{{"a", "[[a]]", 1, false}, {"b", `"b"`, 1, false}},
		},
		{
			name:   "numerals that LuaJIT does not end",
			source: `if x == 3then require("a") end local s = 1e5..require("b")`,
			want:   []call{{"a", `"a"`, 1, false}, {"b", `"b"`, 1, false}},
		},
		{
			name:   "a module name with an escape only Lua 5.1 reads",
			source: "local p = '\\.'\nrequire(\"a\\.b\\\n\")\nrequire \"c\"\n",
			want:   []call{{"a.b\n", "\"a\\.b\\\n\"", 2, false}, {"c", `"c"`, 4, false}},
		},
		{
			// A call's line is the line of its "(" (or of its string or table
			// argument), but for a "(" that starts a line.
			name:   "a call across a line break is on the line of its function",
			source: "local a = 1;;\nrequire\n(\"a\")\npcall\n\n(require, \"b\")\n_G.require\n'c'\n",
			want:   []call{{"a", `"a"`, 2, false}, {"b", `"b"`, 4, true}, {"c", `'c'`, 7, false}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want := make([]parse.RequireInfo, 0, len(tt.want))

			for _, c := range tt.want {
				span := testSpans(t, tt.source, []testLiteral{{name: c.name, text: c.literal, n: 0}})[0]
				want = append(want, parse.RequireInfo{
					Name: c.name, Line: c.line, Static: true, Literal: span, Protected: c.protected, Indirect: "",
				})
			}

			found, err := parse.New().Parse([]byte(tt.source), "test.lua")
			require.NoError(t, err)
			require.Nil(t, found.Fallback, "the parser reads the copy")
			assert.Equal(t, want, found.Requires)
		})
	}
}

// TestParse_CopyFallback: when the parser refuses the copy too, the
// fallback names the line of the error in the source and the token there
// as the source writes it, a string by its value, and an error at the end
// of the file the line of the last token.
func TestParse_CopyFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   parse.Fallback
	}{
		{
			name:   "near a long string",
			source: "local x <const> = 1\nx = [==[\nv]==] [[w]]\n",
			want:   parse.Fallback{Line: 3, Reason: `unexpected symbol near "w"`},
		},
		{
			name:   "near a short string",
			source: "x = 1 // 2 'v'\n",
			want:   parse.Fallback{Line: 1, Reason: `unexpected symbol near "v"`},
		},
		{
			name:   "at the end of the file",
			source: "global x\nfunction f()\n  return 1\n\n-- the end\n\n",
			want: parse.Fallback{
				Line: 3, Reason: "'end' expected (to close 'function' at line 2) at the end of the file",
			},
		},
		{
			name:   "near a numeral LuaJIT does not end",
			source: "x = 1 + 3then\n",
			want:   parse.Fallback{Line: 1, Reason: `unexpected symbol near "then"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			found, err := parse.New().Parse([]byte(tt.source), "test.lua")
			require.NoError(t, err)
			require.NotNil(t, found.Fallback)
			assert.Equal(t, tt.want, *found.Fallback)
		})
	}
}

// TestParse_HeaderWithCarriageReturn: a first line that starts with "#" ends
// at the first "\n", as in PUC Lua, also when it holds a "\r", where LuaJIT
// ends it.
func TestParse_HeaderWithCarriageReturn(t *testing.T) {
	t.Parallel()

	source := "#!/usr/bin/env lua\rrequire('x')\nrequire('a')\n"
	assert.Equal(t, []parse.RequireInfo{testStatic("a", 2)}, testParse(t, source))
}
