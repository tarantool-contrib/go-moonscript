package format_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/format"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
)

var update = flag.Bool("update", false, "rewrite the golden files of testdata/golden")

// TestGolden formats testdata/golden/*.lua and compares with the .golden
// files (go test -update rewrites them): LuaJIT for luajit.lua, Lua 5.1
// for the others; module.lua also with tabs, quotes and trailing commas
// (.options.golden).
func TestGolden(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("testdata", "golden", "*.lua"))
	if len(paths) == 0 {
		t.Fatal("no golden sources")
	}
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		opts := format.Options{}
		if strings.HasSuffix(p, "luajit.lua") {
			opts.Dialect = parse.LuaJIT
		}
		golden(t, p, ".golden", src, opts)
		if strings.HasSuffix(p, "module.lua") {
			golden(t, p, ".options.golden", src, format.Options{UseTabs: true, Quotes: format.QuotesSingle, TrailingComma: true, Width: 80})
		}
	}
}

func golden(t *testing.T, path, suffix string, src []byte, opts format.Options) {
	t.Helper()
	out, err := format.Source(src, opts)
	if err != nil {
		t.Errorf("%s: %v", path, err)
		return
	}
	var st checkStats
	if f, _ := checkSource(src, opts, &st); f != "" {
		t.Errorf("%s: %s", path, f)
	}
	gp := strings.TrimSuffix(path, ".lua") + suffix
	if *update {
		if err := os.WriteFile(gp, out, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(gp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, want) {
		t.Errorf("%s: differs from %s:\n%s", path, gp, firstDiff(want, out))
	}
}
