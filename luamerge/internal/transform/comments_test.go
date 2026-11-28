package transform_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/transform"
)

// testLines is the number of lines of src as Lua counts them.
func testLines(src string) int {
	lines := 1

	for i := 0; i < len(src); {
		if n := lex.LineBreak([]byte(src), i); n > 0 {
			lines++
			i += n
		} else {
			i++
		}
	}

	return lines
}

func TestCommentTransformer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		output string
	}{
		{
			name:   "line comment",
			input:  "print('hello') -- comment\nprint('world')",
			output: "print('hello') \nprint('world')",
		},
		{
			name:   "block comment",
			input:  "print('hello') --[[ block comment ]] print('world')",
			output: "print('hello')  print('world')",
		},
		{
			name:   "nested block comment with equals",
			input:  "print('hello') --[==[ nested ]==] print('world')",
			output: "print('hello')  print('world')",
		},
		{
			name:   "comment in string",
			input:  `str = "-- not a comment" -- actual comment`,
			output: `str = "-- not a comment" `,
		},
		{
			name:   "single quote string",
			input:  `str = '-- not a comment' -- actual comment`,
			output: `str = '-- not a comment' `,
		},
		{
			name:   "escape in string",
			input:  `str = "quote \" -- not comment" -- comment`,
			output: `str = "quote \" -- not comment" `,
		},
		{
			name:   "string ending in an escaped backslash",
			input:  "local sep = \"\\\\\" -- c\nlocal s = \"a -- b\" -- d\n",
			output: "local sep = \"\\\\\" \nlocal s = \"a -- b\" \n",
		},
		{
			name:   "long string",
			input:  `str = [[-- not a comment]] -- comment`,
			output: `str = [[-- not a comment]] `,
		},
		{
			name:   "comment and spaces in a leveled long string",
			input:  "local sql = [=[\nSELECT 1 -- the answer\n]=]\nprint([=[a    b]=]) -- c\n",
			output: "local sql = [=[\nSELECT 1 -- the answer\n]=]\nprint([=[a    b]=]) \n",
		},
		{
			name:   "comment start in a leveled long string",
			input:  "print([==[http://example.com/a--b]==])\n",
			output: "print([==[http://example.com/a--b]==])\n",
		},
		{
			// The long string ends at its first "]]"; nesting is not counted.
			name:   "long bracket opener inside a long string",
			input:  "local open = [[ [[ ]]\nlocal close = \"]]\"\nlocal s = \"a -- b\" -- c\n",
			output: "local open = [[ [[ ]]\nlocal close = \"]]\"\nlocal s = \"a -- b\" \n",
		},
		{
			name:   "multiple comments",
			input:  "a -- c1\nb -- c2\nc",
			output: "a \nb \nc",
		},
		{
			// "localb" would be one name, a global.
			name:   "block comment between a keyword and a name",
			input:  "b = \"global\"\nlocal--[[c]]b = \"local\"\n",
			output: "b = \"global\"\nlocal b = \"local\"\n",
		},
		{
			// "1..2" would be a malformed numeral.
			name:   "block comment between a number and a concatenation",
			input:  "print(1--[[one]]..2)\n",
			output: "print(1 ..2)\n",
		},
		{
			name:   "block comment between tokens that stay apart",
			input:  "a--[[c]]+b",
			output: "a+b",
		},
		{
			// "1y" would be a malformed numeral.
			name:   "block comment between a number and a name",
			input:  "x=1--[[c]]y=2\n",
			output: "x=1 y=2\n",
		},
		{
			// "---[==[" opens no long comment: the third dash is its first
			// character after "--".
			name:   "third dash makes a line comment",
			input:  "x = 1 ---[==[c]==]-1\ny = 2\n",
			output: "x = 1 \ny = 2\n",
		},
		{
			name:   "line comment ended by a carriage return",
			input:  "local x = 1 -- one\rprint(\"x is\", x)\rprint(\"done\")\r",
			output: "local x = 1 \rprint(\"x is\", x)\rprint(\"done\")\r",
		},
		{
			name:   "line comment ended by CR LF",
			input:  "local x = 1 -- one\r\nprint(x)\r\n",
			output: "local x = 1 \r\nprint(x)\r\n",
		},
		{
			// The space keeps "\n" and "\r\n" apart; without it they would
			// still be two line breaks, as "\n\r" and "\n".
			name:   "multi-line block comment keeps its line breaks",
			input:  "a --[[x\ny\r\nz\n\rw\r]] b",
			output: "a \n \r\n\n\r\r b",
		},
		{
			// "\r" and "\n" next to each other would be one line break.
			name:   "line breaks around a removed comment stay apart",
			input:  "a\r--[[\n]]b\n--[[\r]]\nc",
			output: "a\r \nb\n \r \nc",
		},
		{
			name:   "shebang is kept",
			input:  "#!/usr/bin/env lua\n-- c\nprint(1)\n",
			output: "#!/usr/bin/env lua\n\nprint(1)\n",
		},
		{
			name:   "empty input",
			input:  "",
			output: "",
		},
		{
			name:   "only comment",
			input:  "-- comment only",
			output: "",
		},
	}

	transformer := &transform.CommentTransformerForTest{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := transformer.Transform([]byte(tt.input))
			require.NoError(t, err, "Transform() error = %v", err)
			assert.Equal(t, tt.output, string(got), "Transform() = %q, want %q", string(got), tt.output)
			assert.Equal(t, testLines(tt.input), testLines(string(got)), "the line count is kept")
		})
	}
}

func TestCommentTransformer_LexicalError(t *testing.T) {
	t.Parallel()

	transformer := &transform.CommentTransformerForTest{}

	_, err := transformer.Transform([]byte("-- c\nx = \"abc\n"))
	require.ErrorIs(t, err, lex.ErrUnfinishedString)
	require.EqualError(t, err, "remove comments: line 2: unfinished string")
}
