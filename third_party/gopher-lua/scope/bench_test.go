package scope_test

import (
	"testing"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/scope"
)

// benchSources returns the sources of the archives that parse in the
// dialect, with their chunks.
func benchSources(b *testing.B, d scope.Dialect) (srcs [][]byte, chunks []*ast.Chunk, total int) {
	b.Helper()
	files := archiveFiles(b)
	for _, name := range sortedKeys(files) {
		c, err := parse.ParseChunk(files[name], name, parse.Options{Dialect: parseDialect(d)})
		if err != nil {
			continue
		}
		srcs = append(srcs, files[name])
		chunks = append(chunks, c)
		total += len(files[name])
	}
	return srcs, chunks, total
}

// BenchmarkParseArchives is what parsing the archives costs, for comparison
// with BenchmarkResolveArchives.
func BenchmarkParseArchives(b *testing.B) {
	for _, d := range dialects {
		b.Run(d.String(), func(b *testing.B) {
			srcs, _, total := benchSources(b, d)
			b.SetBytes(int64(total))
			b.ReportAllocs()
			for b.Loop() {
				for i, src := range srcs {
					if _, err := parse.ParseChunk(src, "bench", parse.Options{Dialect: parseDialect(d)}); err != nil {
						b.Fatal(i, err)
					}
				}
			}
		})
	}
}

// BenchmarkResolveArchives is what Resolve costs over the trees of the
// archives, parsed once.
func BenchmarkResolveArchives(b *testing.B) {
	for _, d := range dialects {
		b.Run(d.String(), func(b *testing.B) {
			_, chunks, total := benchSources(b, d)
			b.SetBytes(int64(total))
			b.ReportAllocs()
			for b.Loop() {
				for i, c := range chunks {
					if _, err := scope.Resolve(c, d); err != nil {
						b.Fatal(i, err)
					}
				}
			}
		})
	}
}
