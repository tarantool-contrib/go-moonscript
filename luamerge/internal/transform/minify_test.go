package transform_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/transform"
)

// testCode is the tokens of src but the comments, as their texts.
func testCode(t *testing.T, src string) []string {
	t.Helper()

	tokens, err := lex.NewFile([]byte(src)).Tokens()
	require.NoError(t, err, "lex %q", src)

	var code []string

	for _, tok := range tokens {
		if tok.Kind != lex.Comment && tok.Kind != lex.LongComment {
			code = append(code, src[tok.Start:tok.End])
		}
	}

	return code
}

func TestMinifyTransformer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		output string
	}{
		{
			name:   "comments and spaces",
			input:  "local  x  =  5  -- comment\nprint(x)",
			output: "local x=5\nprint(x)",
		},
		{
			name:   "multiple spaces",
			input:  "a     b     c",
			output: "a b c",
		},
		{
			name:   "tabs and spaces",
			input:  "a \t  b\t\tc",
			output: "a b c",
		},
		{
			name:   "spaces in string",
			input:  `str = "a     b"`,
			output: `str="a     b"`,
		},
		{
			name:   "trailing space before newline",
			input:  "line1   \nline2",
			output: "line1\nline2",
		},
		{
			// "ab" would be one name.
			name:   "block comment between names",
			input:  "a--[[comment]]b",
			output: "a b",
		},
		{
			name:   "empty lines",
			input:  "a\n\nb\n  \nc",
			output: "a\nb\nc",
		},
		{
			name:   "blank lines in a long string",
			input:  "local help = [[\nUsage: tool [options]\n\nOptions:\n  -h   show help]]\nprint(#help)\n",
			output: "local help=[[\nUsage: tool [options]\n\nOptions:\n  -h   show help]]\nprint(#help)\n",
		},
		{
			name:   "comment and spaces in a leveled long string",
			input:  "local sql = [=[\nSELECT 1 -- the answer\n]=]\nio.write(sql)\nprint([=[a    b]=])\n",
			output: "local sql=[=[\nSELECT 1 -- the answer\n]=]\nio.write(sql)\nprint([=[a    b]=])\n",
		},
		{
			name:   "comment start in a leveled long string",
			input:  "print([==[http://example.com/a--b]==])\n",
			output: "print([==[http://example.com/a--b]==])\n",
		},
		{
			name:   "string ending in an escaped backslash",
			input:  "local sep = \"\\\\\"\nlocal banner = \"|    centered    |\"\nprint(sep, banner, #banner)\n",
			output: "local sep=\"\\\\\"\nlocal banner=\"|    centered    |\"\nprint(sep,banner,#banner)\n",
		},
		{
			name:   "block comment between a keyword and a name",
			input:  "b = \"global\"\nlocal--[[c]]b = \"local\"\nprint(b, _G.b)\n",
			output: "b=\"global\"\nlocal b=\"local\"\nprint(b,_G.b)\n",
		},
		{
			name:   "block comment between a number and a concatenation",
			input:  "print(1--[[one]]..2)\n",
			output: "print(1 ..2)\n",
		},
		{
			name:   "long bracket opener inside a long string",
			input:  "local open = [[ [[ ]]\nlocal close = \"]]\"\nlocal s = \"a -- b\"\nprint(open, close, s)\n",
			output: "local open=[[ [[ ]]\nlocal close=\"]]\"\nlocal s=\"a -- b\"\nprint(open,close,s)\n",
		},
		{
			name:   "line comment ended by a carriage return",
			input:  "local x = 1 -- one\rprint(\"x is\", x)\rprint(\"done\")\r",
			output: "local x=1\nprint(\"x is\",x)\nprint(\"done\")\n",
		},
		{
			name: "tokens that would run together",
			//nolint:dupword // "not not" is Lua, and the point of the case.
			input: "x = a - -b .. 1 .. 2 .. c\ny = t[ [[s]] ] == u\nreturn not not z",
			//nolint:dupword // "not not" is Lua, and the point of the case.
			output: "x=a- -b..1 ..2 ..c\ny=t[ [[s]]]==u\nreturn not not z",
		},
		{
			name:   "numerals and their signs",
			input:  "x = 1e-5 - 0x1p4 + .5 - 1",
			output: "x=1e-5-0x1p4+.5-1",
		},
		{
			name:   "labels",
			input:  "x = a < b\ngoto skip ::skip::",
			output: "x=a<b\ngoto skip::skip::",
		},
		{
			// Lua 5.1 and LuaJIT refuse the call as ambiguous; LuaJIT built with
			// LUAJIT_ENABLE_LUA52COMPAT reads it.
			name:   "line break before a parenthesis",
			input:  "local f = g\n(h)()",
			output: "local f=g\n(h)()",
		},
		{
			name:   "line break inside a long comment",
			input:  "a = 1 --[[\n]] b = 2",
			output: "a=1\nb=2",
		},
		{
			name:   "line breaks inside a string are part of it",
			input:  "s = \"a\\z\n  b\" t = 1",
			output: "s=\"a\\z\n  b\"t=1",
		},
		{
			name:   "CR LF line endings",
			input:  "a = 1\r\n\r\nb = 2\r\n",
			output: "a=1\nb=2\n",
		},
		{
			name:   "byte order mark and shebang",
			input:  lex.BOM + "#!/usr/bin/env lua\n\n  print(1) -- one\n",
			output: "print(1)\n",
		},
		{
			name:   "comments only",
			input:  "-- a\n--[[ b ]]\n",
			output: "",
		},
		{
			// The source ends in a comment, with no line break after it.
			name:   "line break before a comment at the end",
			input:  "x = 1\n-- end",
			output: "x=1\n",
		},
		{
			name:   "line break inside a long comment at the end",
			input:  "x = 1 --[[\n]]",
			output: "x=1\n",
		},
	}

	transformer := &transform.MinifyTransformerForTest{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := transformer.Transform([]byte(tt.input))
			require.NoError(t, err, "Transform() error = %v", err)
			assert.Equal(t, tt.output, string(got), "Transform() = %q, want %q", string(got), tt.output)
			assert.Equal(t, testCode(t, tt.input), testCode(t, string(got)), "the tokens are kept")
		})
	}
}

func TestMinifyTransformer_LexicalError(t *testing.T) {
	t.Parallel()

	transformer := &transform.MinifyTransformerForTest{}

	_, err := transformer.Transform([]byte("x = 1\n--[==[ c ]]\n"))
	require.ErrorIs(t, err, lex.ErrUnfinishedLongComment)
	require.EqualError(t, err, "minify: line 2: unfinished long comment")
}
