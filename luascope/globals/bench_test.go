package globals_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
	"github.com/tarantool-contrib/go-moonscript/luascope"
	"github.com/tarantool-contrib/go-moonscript/luascope/globals"
)

// acceptedSources returns the Lua files of the archives that parse, in
// the order of their names: 6.2 MB in 961 files.
func acceptedSources(b *testing.B) [][]byte {
	b.Helper()

	var srcs [][]byte

	for _, suite := range []string{"lua5.1-tests", "luajit-tests", "tarantool", "vshard", "crud"} {
		files := readArchive(b, suite)
		names := make([]string, 0, len(files))

		for name := range files {
			if strings.HasSuffix(name, ".lua") || strings.HasSuffix(name, ".t") {
				names = append(names, name)
			}
		}

		slices.Sort(names)

		for _, name := range names {
			if _, _, err := luascope.Parse(files[name], "=m", luascope.Options{}); err == nil {
				srcs = append(srcs, files[name])
			}
		}
	}

	return srcs
}

func size(srcs [][]byte) int64 {
	n := 0
	for _, src := range srcs {
		n += len(src)
	}

	return int64(n)
}

// BenchmarkAnalyze parses, resolves and analyzes the archives, one call of
// Analyze per file: what a tool checking files one by one does.
func BenchmarkAnalyze(b *testing.B) {
	srcs := acceptedSources(b)

	b.SetBytes(size(srcs))
	b.ReportAllocs()

	for b.Loop() {
		for _, src := range srcs {
			if _, err := globals.Analyze(src, "=m", luascope.Options{}); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkAnalyzeChunk analyzes the archives parsed and resolved once:
// the analysis alone.
func BenchmarkAnalyzeChunk(b *testing.B) {
	srcs := acceptedSources(b)

	type parsed struct {
		chunk *ast.Chunk
		info  *scope.Info
	}

	chunks := make([]parsed, len(srcs))

	for i, src := range srcs {
		chunk, info, err := luascope.Parse(src, "=m", luascope.Options{})
		if err != nil {
			b.Fatal(err)
		}

		chunks[i] = parsed{chunk, info}
	}

	b.SetBytes(size(srcs))
	b.ReportAllocs()

	for b.Loop() {
		for _, c := range chunks {
			globals.AnalyzeChunk(c.chunk, c.info)
		}
	}
}
