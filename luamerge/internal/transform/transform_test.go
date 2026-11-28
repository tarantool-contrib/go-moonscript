package transform_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/config"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/transform"
)

// testApply runs source through the transformers of pipeline in order, as the
// emitter does.
func testApply(t *testing.T, pipeline []transform.Transformer, source string) string {
	t.Helper()

	out := []byte(source)

	for _, transformer := range pipeline {
		var err error

		out, err = transformer.Transform(out)
		require.NoError(t, err)
	}

	return string(out)
}

// TestBuildPipeline checks which transforms each configuration applies and in
// which order. Comments go before empty lines, so a line that held nothing but
// a comment goes as well; the other way round it stayed as an empty line.
// Minify takes the place of both.
func TestBuildPipeline(t *testing.T) {
	t.Parallel()

	const source = "local a = 1 -- one\n-- a comment line\n\nlocal b = 2\n"

	tests := []struct {
		name string
		cfg  config.TransformConfig
		want string
	}{
		{
			name: "none",
			cfg:  config.TransformConfig{RemoveComments: false, RemoveEmptyLines: false, Minify: false},
			want: source,
		},
		{
			name: "comments",
			cfg:  config.TransformConfig{RemoveComments: true, RemoveEmptyLines: false, Minify: false},
			want: "local a = 1 \n\n\nlocal b = 2\n",
		},
		{
			name: "empty lines",
			cfg:  config.TransformConfig{RemoveComments: false, RemoveEmptyLines: true, Minify: false},
			want: "local a = 1 -- one\n-- a comment line\nlocal b = 2\n",
		},
		{
			name: "comments, then empty lines",
			cfg:  config.TransformConfig{RemoveComments: true, RemoveEmptyLines: true, Minify: false},
			want: "local a = 1 \nlocal b = 2\n",
		},
		{
			name: "minify",
			cfg:  config.TransformConfig{RemoveComments: false, RemoveEmptyLines: false, Minify: true},
			want: "local a=1\nlocal b=2\n",
		},
		{
			name: "minify with the others",
			cfg:  config.TransformConfig{RemoveComments: true, RemoveEmptyLines: true, Minify: true},
			want: "local a=1\nlocal b=2\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testApply(t, transform.BuildPipeline(tt.cfg), source))
		})
	}
}
