package parse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
)

// TestParse_DialectSyntax: the requires of a file in the syntax of any
// supported dialect are found on their lines: LuaJIT's own numerals, escapes
// and names beyond ASCII, goto as a statement and as a name, what LuaJIT
// built with LUAJIT_ENABLE_LUA52COMPAT reads (a statement after break, an
// empty statement and a call across a line break), and what only Lua 5.1
// reads: a string with an escape LuaJIT does not know, and numerals that
// LuaJIT does not end where Lua 5.1 does.
func TestParse_DialectSyntax(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		// Numerals.
		{
			name:   "hexadecimal floats",
			source: "local v = 0x1p4 + 0x.8p1 + 0xA.8P-2 + require(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1)},
		},
		{
			name:   "LuaJIT integer, binary and imaginary numerals",
			source: "local v = 1ULL + 0x10LL + 12ull + 2i + 1.5i + 0b101 + require(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1)},
		},
		{
			name:   "Lua 5.1 numeral before a concatenation",
			source: "local s = 1e5..require(\"a\")\nlocal t = 0x10..require(\"b\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2)},
		},
		{
			name:   "Lua 5.1 numeral out of range",
			source: "local v = 1e1048576 + require(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1)},
		},

		// Names.
		{
			name:   "names beyond ASCII",
			source: "local \xD0\xBF = require(\"a\")\n\xD0\xBF.x = require(\"b\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2)},
		},
		{
			name:   "goto as a name",
			source: "local goto = require(\"a\")\nlocal t = { goto = goto }\nt.goto = require(\"b\")\nprint(goto)\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 3)},
		},
		{
			name:   "goto statement",
			source: "goto skip\nrequire(\"a\")\n::skip::\nrequire(\"b\")\n",
			want:   []parse.RequireInfo{testStatic("a", 2), testStatic("b", 4)},
		},
		{
			// LuaJIT reads goto as a name next to the goto statements.
			name: "goto as a name and goto statements",
			//nolint:dupword // "goto goto" is Lua, and the point of the case.
			source: "local goto = require(\"a\")\ngoto skip\n::skip::\nx = goto goto skip\nrequire(\"b\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 5)},
		},

		// What LuaJIT builds with LUAJIT_ENABLE_LUA52COMPAT read.
		{
			name:   "statement after break",
			source: "for i = 1, 2 do\n  break\n  require(\"a\")\nend\n",
			want:   []parse.RequireInfo{testStatic("a", 3)},
		},
		{
			name:   "call across a line break",
			source: "local f = function() return print end\nf()\n(require(\"a\"))\n",
			want:   []parse.RequireInfo{testStatic("a", 3)},
		},
		{
			name:   "call across a line break after a long string",
			source: "local f = function() return print end\nf [[\n]](require(\"a\"))\nf \"b\"\n(require(\"b\"))\n",
			want:   []parse.RequireInfo{testStatic("a", 3), testStatic("b", 5)},
		},
		{
			name: "empty statements",
			source: ";local a = require(\"a\");;\n" +
				"do; require(\"b\") end\n" +
				"if a then ; elseif b then;; else ; end\n" +
				"repeat ; until true\n" +
				"local t = { 1; 2; require(\"c\"); }\n",
			want: []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2), testStatic("c", 5)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testParse(t, tt.source))
		})
	}
}

// TestParse_Lua51Strings: a file with an escape in a string that only Lua 5.1
// reads, which reads it as the character after the backslash, is read in
// Lua 5.1's dialect: every module name is where it is in the file, on its
// line, and a name with such an escape is the name Lua 5.1 reads.
func TestParse_Lua51Strings(t *testing.T) {
	t.Parallel()

	type call struct {
		name, literal string
		line          int
	}

	tests := []struct {
		name   string
		source string
		want   []call
	}{
		{
			name:   "a string before the literal",
			source: "local p = '\\.'\nrequire(\"a\\.b\\\n\")\nrequire \"c\"\n",
			want:   []call{{"a.b\n", "\"a\\.b\\\n\"", 2}, {"c", `"c"`, 4}},
		},
		{
			name:   "unknown escapes where LuaJIT knows others",
			source: `local p = "\xZZ" .. "\u{110000}" .. "\u{D800}"; require [[a]] require"\q\x41"`,
			want:   []call{{"a", "[[a]]", 1}, {"qx41", `"\q\x41"`, 1}},
		},
		{
			name:   "numerals LuaJIT does not end",
			source: "local s = 1e5..require(\"a\")\nlocal t = 0x10..require(\"b\")\nrequire(\"\\.c\")",
			want:   []call{{"a", `"a"`, 1}, {"b", `"b"`, 2}, {".c", `"\.c"`, 3}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want := make([]parse.RequireInfo, 0, len(tt.want))

			for _, c := range tt.want {
				span := testSpans(t, tt.source, []testLiteral{{name: c.name, text: c.literal, n: 0}})[0]
				want = append(want, parse.RequireInfo{
					Name: c.name, Line: c.line, Static: true, Literal: span, Protected: false, Indirect: "",
				})
			}

			found, err := parse.New().Parse([]byte(tt.source), "test.lua")
			require.NoError(t, err)
			require.Nil(t, found.Fallback, "Lua 5.1's dialect reads the file")
			assert.Equal(t, want, found.Requires)
		})
	}
}

// TestParse_CallAcrossLineBreak: in the syntax of LuaJIT's LJ_52 builds a
// call whose "(" starts a line is on the line of its function, where the
// parser numbers it by its "(", and the literal of its module name is where
// it is.
func TestParse_CallAcrossLineBreak(t *testing.T) {
	t.Parallel()

	type call struct {
		name, literal string
		line          int
		protected     bool
	}

	source := "local a = 1;;\nrequire\n(\"a\")\npcall\n\n(require, \"b\")\n_G.require\n'c'\n"
	want := []call{{"a", `"a"`, 2, false}, {"b", `"b"`, 4, true}, {"c", `'c'`, 7, false}}
	wantInfo := make([]parse.RequireInfo, 0, len(want))

	for _, c := range want {
		span := testSpans(t, source, []testLiteral{{name: c.name, text: c.literal, n: 0}})[0]
		wantInfo = append(wantInfo, parse.RequireInfo{
			Name: c.name, Line: c.line, Static: true, Literal: span, Protected: c.protected, Indirect: "",
		})
	}

	found, err := parse.New().Parse([]byte(source), "test.lua")
	require.NoError(t, err)
	require.Nil(t, found.Fallback, "the parser reads the file")
	assert.Equal(t, wantInfo, found.Requires)
}
