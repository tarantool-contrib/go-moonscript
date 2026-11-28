package luascope_test

import (
	"errors"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lua/parse"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
	"github.com/tarantool-contrib/go-moonscript/luascope"
	"github.com/tarantool-contrib/go-moonscript/luascope/allowlist"
	"github.com/tarantool-contrib/go-moonscript/luascope/globals"
)

// FuzzParse checks that Parse never panics and reports its errors with
// their types, and that the analyses of what it parses never panic and
// give every access the span of the name it starts with.
func FuzzParse(f *testing.F) {
	for _, c := range snippets {
		f.Add(c.src)
	}

	checker, err := allowlist.NewChecker(allowlist.Rules{
		Modules: []string{"log", "crud.*"},
		Globals: []string{"require", "box.space.*", "_G.*"},
	})
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		chunk, info, err := luascope.Parse(src, "=fuzz", luascope.Options{})
		if err != nil {
			var (
				pe *parse.Error
				se *scope.Error
			)

			known := errors.As(err, &pe) || errors.As(err, &se) ||
				errors.Is(err, luascope.ErrBytecode)
			if !known {
				t.Fatalf("unexpected error type %T: %v", err, err)
			}

			return
		}

		for _, a := range globals.AnalyzeChunk(chunk, info).Accesses {
			name := string(src[a.Span.Start:a.Span.End])
			if a.Pos.Offset != a.Span.Start || !a.Pos.IsValid() || name != a.Path[0].Name {
				t.Fatalf("bad span %v of %s at %v", a.Span, a.Path, a.Pos)
			}
		}

		checker.CheckChunk(chunk, info)
	})
}
