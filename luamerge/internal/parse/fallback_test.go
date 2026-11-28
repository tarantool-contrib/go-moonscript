package parse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
)

// TestParse_Fallback: when no dialect the parser reads accepts the file, the
// fallback names the line of the error in the source and the token there as
// the source writes it, a string by its value, and an error at the end of the
// file the line of the last token. The error is LuaJIT's, but for a file
// with a string or a numeral that only Lua 5.1 reads: its error is Lua 5.1's
// where that got further, as LuaJIT's is only that it does not know the
// escape or the numeral.
func TestParse_Fallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   parse.Fallback
	}{
		{
			name:   "near a long string",
			source: "local x = 1\nx = [==[\nv]==] [[w]]\n",
			want:   parse.Fallback{Line: 3, Reason: `unexpected symbol near "w"`},
		},
		{
			name:   "near a short string",
			source: "x = 1 'v'\n",
			want:   parse.Fallback{Line: 1, Reason: `unexpected symbol near "v"`},
		},
		{
			name:   "at the end of the file",
			source: "local x = 1\nfunction f()\n  return 1\n\n-- the end\n\n",
			want: parse.Fallback{
				Line: 3, Reason: "'end' expected (to close 'function' at line 2) at the end of the file",
			},
		},
		{
			name:   "the error of Lua 5.1 after an escape LuaJIT does not know",
			source: "local p = '\\.'\nx = 1 = 2\n",
			want:   parse.Fallback{Line: 2, Reason: `unexpected symbol near "="`},
		},
		{
			name:   "the error of Lua 5.1 after a numeral LuaJIT does not end",
			source: "local s = 1e5..x\nx = 1 = 2\n",
			want:   parse.Fallback{Line: 2, Reason: `unexpected symbol near "="`},
		},
		{
			name:   "the error of LuaJIT after a numeral only it reads",
			source: "local s = 0b101\nx = 1 = 2\n",
			want:   parse.Fallback{Line: 2, Reason: `unexpected symbol near "="`},
		},
		{
			// Lua 5.1 numbers the error at the end of the block.
			name:   "the error of LuaJIT for a break outside a loop",
			source: "local x = 1\nif x then\n  break\nend\n",
			want:   parse.Fallback{Line: 3, Reason: "no loop to break"},
		},
		{
			name:   "syntax LuaJIT does not read",
			source: "local a = 1 != 2\n",
			want:   parse.Fallback{Line: 1, Reason: `unexpected symbol near "!"`},
		},
		{
			name:   "Lua 5.3 operators",
			source: "local a = 7 // 2\n",
			want:   parse.Fallback{Line: 1, Reason: `unexpected symbol near "/"`},
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

// TestParse_Lua51Header: a file read in Lua 5.1's dialect, as it reads a
// file, skips a first line that starts with "#" and numbers the lines from it.
func TestParse_Lua51Header(t *testing.T) {
	t.Parallel()

	source := "#!/usr/bin/env lua\nlocal p = '\\.'\nrequire('a')\n"
	assert.Equal(t, []parse.RequireInfo{testStatic("a", 3)}, testParse(t, source))
}
