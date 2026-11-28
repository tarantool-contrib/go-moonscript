package format_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lua/format"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
)

// fuzzSeeds adds the small compatibility scripts and the hazards of the
// tests to the corpus of a fuzz target.
func fuzzSeeds(f *testing.F) {
	paths, _ := filepath.Glob(filepath.Join("..", "testdata", "compat", "*.lua"))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err == nil && len(b) < 4096 {
			f.Add(b, false)
		}
	}
	for _, c := range hazardSources {
		f.Add([]byte(c), true)
	}
	for _, c := range commentSources {
		f.Add([]byte(c), true)
	}
}

func fuzzDialect(jit bool) parse.Dialect {
	if jit {
		return parse.LuaJIT
	}
	return parse.Lua51
}

// FuzzFormat checks the formatter on sources that parse and compile: the
// formatted source compiles to the same code, formatting it again changes
// nothing, its comments and literals are those of the source.
func FuzzFormat(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, src []byte, jit bool) {
		opts := format.Options{Dialect: fuzzDialect(jit), Width: 60}
		if len(src) > 0 && src[0]&1 == 1 {
			opts.TrailingComma, opts.KeepSemicolons, opts.UseTabs = true, true, true
		}
		var st checkStats
		if f, _ := checkSource(src, opts, &st); f != "" {
			t.Fatalf("%s\nsource:\n%s", f, src)
		}
	})
}

// FuzzPrint checks the printer on the trees of sources that parse and
// compile: the printed tree compiles to the same code, and printing the
// tree of the text gives the same text.
func FuzzPrint(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, src []byte, jit bool) {
		opts := format.Options{Dialect: fuzzDialect(jit), Width: 50}
		want, err := compile(src, opts.Dialect)
		if err != nil {
			return
		}
		if f := checkPrinter(src, want, opts); f != "" {
			t.Fatalf("%s\nsource:\n%s", f, src)
		}
	})
}
