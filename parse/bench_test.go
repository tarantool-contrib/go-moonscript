package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkUpstream parses all upstream sources of the golden corpus (the
// ones the reference parses successfully) concatenated into one file.
func BenchmarkUpstream(b *testing.B) {
	trees, err := filepath.Glob("../testdata/parse/upstream/*.tree")
	if err != nil || len(trees) == 0 {
		b.Fatal("no corpus", err)
	}
	var sb strings.Builder
	for _, t := range trees {
		src, err := os.ReadFile(strings.TrimSuffix(t, ".tree") + ".moon")
		if err != nil {
			b.Fatal(err)
		}
		sb.Write(src)
	}
	src := sb.String()
	if _, err := String(src); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := String(src); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFiles parses each file listed in $MOON_BENCH_FILES
// (colon-separated paths), one sub-benchmark per file.
func BenchmarkFiles(b *testing.B) {
	list := os.Getenv("MOON_BENCH_FILES")
	if list == "" {
		b.Skip("MOON_BENCH_FILES not set")
	}
	for _, path := range filepath.SplitList(list) {
		data, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		src := string(data)
		b.Run(filepath.Base(path), func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			for i := 0; i < b.N; i++ {
				_, _ = String(src) // files that fail to parse are benchmarked too
			}
		})
	}
}
