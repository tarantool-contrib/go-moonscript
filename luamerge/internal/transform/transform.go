package transform

import "github.com/tarantool/go-luamerge/internal/config"

// Transformer applies a source-to-source transformation to Lua source code.
type Transformer interface {
	Transform(source []byte) ([]byte, error)
}

// BuildPipeline builds the ordered list of Transformers to apply, based on
// the given transform configuration.
func BuildPipeline(cfg config.TransformConfig) []Transformer {
	var pipeline []Transformer

	if cfg.Minify {
		pipeline = append(pipeline, &minifyTransformer{})
	} else {
		if cfg.RemoveComments {
			pipeline = append(pipeline, &commentTransformer{})
		}

		if cfg.RemoveEmptyLines {
			pipeline = append(pipeline, &emptyLineTransformer{})
		}
	}

	return pipeline
}
