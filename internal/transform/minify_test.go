package transform_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/transform"
)

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
			output: "local x = 5\nprint(x)",
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
			output: `str = "a     b"`,
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
	}

	transformer := &transform.MinifyTransformerForTest{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := transformer.Transform([]byte(tt.input))
			require.NoError(t, err, "Transform() error = %v", err)
			assert.Equal(t, tt.output, string(got), "Transform() = %q, want %q", string(got), tt.output)
		})
	}
}
