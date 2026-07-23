package transform_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/transform"
)

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
	}

	transformer := &transform.EmptyLineTransformerForTest{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := transformer.Transform([]byte(tt.input))
			require.NoError(t, err, "Transform() error = %v", err)
			assert.Equal(t, tt.output, string(got), "Transform() = %q, want %q", string(got), tt.output)
		})
	}
}
