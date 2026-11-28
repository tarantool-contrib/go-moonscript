package flow_test

import (
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lua/flow"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

// benchInfos returns the resolutions of the sources of the archives that
// parse in the dialect.
func benchInfos(b *testing.B, d scope.Dialect) (infos []*scope.Info, total int) {
	b.Helper()
	files := archiveFiles(b)
	for _, name := range sortedKeys(files) {
		c, err := parse.ParseChunk(files[name], name, parse.Options{Dialect: parseDialect(d)})
		if err != nil {
			continue
		}
		info, err := scope.Resolve(c, d)
		if err != nil {
			b.Fatal(name, err)
		}
		infos = append(infos, info)
		total += len(files[name])
	}
	return infos, total
}

// BenchmarkGraphsArchives is what building the graphs of the archives'
// trees costs, resolved once.
func BenchmarkGraphsArchives(b *testing.B) {
	benchAnalyze(b, flow.GraphsOnly())
}

// BenchmarkAnalyzeArchives is what Analyze, the graphs and the data flow,
// costs over the archives' trees, resolved once.
func BenchmarkAnalyzeArchives(b *testing.B) {
	benchAnalyze(b, nil)
}

// BenchmarkQueriesArchives is what the queries of a lint pass cost over the
// archives' Programs, built once: the unreachable code, the returns of each
// function, the unused recursive functions.
func BenchmarkQueriesArchives(b *testing.B) {
	for _, d := range dialects {
		b.Run(d.String(), func(b *testing.B) {
			infos, total := benchInfos(b, d)
			progs := make([]*flow.Program, len(infos))
			for i, info := range infos {
				progs[i] = flow.Analyze(info, nil)
			}
			b.SetBytes(int64(total))
			b.ReportAllocs()
			for b.Loop() {
				for _, p := range progs {
					for _, g := range p.Graphs {
						g.Unreachable()
						g.Returns()
					}
					p.UnusedRecursive()
				}
			}
		})
	}
}

func benchAnalyze(b *testing.B, opts *flow.Options) {
	for _, d := range dialects {
		b.Run(d.String(), func(b *testing.B) {
			infos, total := benchInfos(b, d)
			b.SetBytes(int64(total))
			b.ReportAllocs()
			for b.Loop() {
				for _, info := range infos {
					flow.Analyze(info, opts)
				}
			}
		})
	}
}
