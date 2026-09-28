package transform_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/lex"
	"github.com/tarantool/go-luamerge/internal/transform"
)

// testNonSpace is src without its white space.
func testNonSpace(src string) string {
	var kept strings.Builder

	for i := range len(src) {
		if !lex.IsSpace(src[i]) {
			kept.WriteByte(src[i])
		}
	}

	return kept.String()
}

func TestEmptyLineTransformer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		output string
	}{
		{
			name:   "empty lines",
			input:  "line1\n\nline2\n  \nline3",
			output: "line1\nline2\nline3",
		},
		{
			name:   "trailing newline",
			input:  "line1\nline2\n",
			output: "line1\nline2\n",
		},
		{
			name:   "no trailing newline",
			input:  "line1\nline2",
			output: "line1\nline2",
		},
		{
			name:   "only whitespace lines",
			input:  "  \n\t\n  \n",
			output: "",
		},
		{
			name:   "mixed",
			input:  "a\n\nb\n  \nc\n\nd",
			output: "a\nb\nc\nd",
		},
		{
			name:   "empty input",
			input:  "",
			output: "",
		},
		{
			name:   "blank lines in a long string",
			input:  "local help = [[\nUsage: tool [options]\n\nOptions:\n  -h   show help]]\nprint(#help)\n",
			output: "local help = [[\nUsage: tool [options]\n\nOptions:\n  -h   show help]]\nprint(#help)\n",
		},
		{
			name:   "blank lines in a leveled long string and a long comment",
			input:  "--[==[\n\n]==]\nlocal s = [=[\n\n  \n]=]\n\n\nprint(s)\n",
			output: "--[==[\n\n]==]\nlocal s = [=[\n\n  \n]=]\nprint(s)\n",
		},
		{
			name:   "blank line after a z escape",
			input:  "local s = \"a\\z\n\n  b\"\n\nprint(s)\n",
			output: "local s = \"a\\z\n\n  b\"\nprint(s)\n",
		},
		{
			name:   "indentation and trailing white space stay",
			input:  "do   \n\n\t\n    x()\nend\n",
			output: "do   \n    x()\nend\n",
		},
		{
			name:   "leading blank lines",
			input:  "\n\n  \n  x = 1\n",
			output: "  x = 1\n",
		},
		{
			// The line break of the last line with a token stays.
			name:   "white space after the last line break",
			input:  "t[1]\n  ",
			output: "t[1]\n",
		},
		{
			// The white space around the tokens of a line stays, on the first
			// and the last line of the source too.
			name:   "white space on the first and the last line",
			input:  "  x = 1\n\ny = 2  ",
			output: "  x = 1\ny = 2  ",
		},
		{
			name:   "CR line endings",
			input:  "a\r\r  \rb\r",
			output: "a\rb\r",
		},
		{
			name:   "CR LF line endings",
			input:  "a\r\n\r\n  \r\nb\r\n",
			output: "a\r\nb\r\n",
		},
		{
			name:   "LF CR line endings",
			input:  "a\n\r\n\r\n\rb",
			output: "a\n\rb",
		},
		{
			// "\r\n" and "\n\r": two line breaks, one empty line between them.
			name:   "mixed line endings",
			input:  "a\r\n\n\rb",
			output: "a\r\nb",
		},
		{
			name:   "comment lines stay",
			input:  "a\n-- c\n\nb\n",
			output: "a\n-- c\nb\n",
		},
		{
			name:   "shebang stays",
			input:  "#!/usr/bin/env lua\n\nprint(1)\n",
			output: "#!/usr/bin/env lua\nprint(1)\n",
		},
	}

	transformer := &transform.EmptyLineTransformerForTest{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := transformer.Transform([]byte(tt.input))
			require.NoError(t, err, "Transform() error = %v", err)
			assert.Equal(t, tt.output, string(got), "Transform() = %q, want %q", string(got), tt.output)
			assert.Equal(t, testNonSpace(tt.input), testNonSpace(string(got)), "only white space is removed")
		})
	}
}

func TestEmptyLineTransformer_LexicalError(t *testing.T) {
	t.Parallel()

	transformer := &transform.EmptyLineTransformerForTest{}

	_, err := transformer.Transform([]byte("\n\nx = [[abc\n"))
	require.ErrorIs(t, err, lex.ErrUnfinishedLongString)
	require.EqualError(t, err, "remove empty lines: line 3: unfinished long string")
}
