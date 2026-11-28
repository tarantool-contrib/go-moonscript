package format_test

import (
	"io"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/format"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
)

// benchSources are the archives' sources that parse under LuaJIT.
func benchSources(b *testing.B) (srcs [][]byte, size int64) {
	files := archiveFiles(b)
	for _, name := range sortedKeys(files) {
		src := files[name]
		if _, err := parse.ParseChunk(src, "bench", parse.Options{Dialect: parse.LuaJIT, Shebang: true}); err == nil {
			srcs = append(srcs, src)
			size += int64(len(src))
		}
	}
	return srcs, size
}

// BenchmarkSourceArchives formats the luasuites archives: parsing with
// the tokens, attaching the comments, printing.
func BenchmarkSourceArchives(b *testing.B) {
	srcs, size := benchSources(b)
	opts := format.Options{Dialect: parse.LuaJIT}
	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, src := range srcs {
			if _, err := format.Source(src, opts); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkParseTokensArchives is the parse that Source starts with, for
// comparison.
func BenchmarkParseTokensArchives(b *testing.B) {
	srcs, size := benchSources(b)
	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, src := range srcs {
			if _, err := parse.ParseChunk(src, "bench", parse.Options{Dialect: parse.LuaJIT, Shebang: true, Tokens: true}); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkCommentMapArchives attaches the comments of the parsed
// archives.
func BenchmarkCommentMapArchives(b *testing.B) {
	srcs, size := benchSources(b)
	var chunks []*ast.Chunk
	for _, src := range srcs {
		c, _ := parse.ParseChunk(src, "bench", parse.Options{Dialect: parse.LuaJIT, Shebang: true, Tokens: true})
		chunks = append(chunks, c)
	}
	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, c := range chunks {
			format.NewCommentMap(c)
		}
	}
}

// BenchmarkNodeArchives prints the parsed trees of the archives in the
// canonical style (bytes: of the sources).
func BenchmarkNodeArchives(b *testing.B) {
	srcs, size := benchSources(b)
	var chunks []*ast.Chunk
	for _, src := range srcs {
		c, _ := parse.ParseChunk(src, "bench", parse.Options{Dialect: parse.LuaJIT, Shebang: true})
		chunks = append(chunks, c)
	}
	opts := format.Options{Dialect: parse.LuaJIT}
	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, c := range chunks {
			if err := format.Node(io.Discard, c, opts); err != nil {
				b.Fatal(err)
			}
		}
	}
}
