package flow_test

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/flow"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/scope"
)

var dialects = []scope.Dialect{scope.Lua51, scope.LuaJIT}

func parseDialect(d scope.Dialect) parse.Dialect {
	if d == scope.LuaJIT {
		return parse.LuaJIT
	}
	return parse.Lua51
}

// analyze parses, resolves and analyzes src.
func analyze(tb testing.TB, src string, d scope.Dialect, opts *flow.Options) (*ast.Chunk, *scope.Info, *flow.Program) {
	tb.Helper()
	chunk, err := parse.ParseChunk([]byte(src), "test", parse.Options{Dialect: parseDialect(d)})
	if err != nil {
		tb.Fatalf("%v: parse %q: %v", d, src, err)
	}
	info, err := scope.Resolve(chunk, d)
	if err != nil {
		tb.Fatalf("%v: resolve %q: %v", d, src, err)
	}
	return chunk, info, flow.Analyze(info, opts)
}

// parseAny parses src as LuaJIT, else as Lua 5.1; ok is false if neither
// parses it.
func parseAny(src []byte, name string) (chunk *ast.Chunk, info *scope.Info, d scope.Dialect, ok bool) {
	for _, d := range []scope.Dialect{scope.LuaJIT, scope.Lua51} {
		c, err := parse.ParseChunk(src, name, parse.Options{Dialect: parseDialect(d)})
		if err != nil {
			continue
		}
		in, err := scope.Resolve(c, d)
		if err != nil {
			continue
		}
		return c, in, d, true
	}
	return nil, nil, 0, false
}

// luaFiles returns the Lua files under dir (and its subdirectories), by
// path.
func luaFiles(tb testing.TB, dir string) map[string][]byte {
	tb.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".lua") {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = b
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	return files
}

// archivesDir holds go-moonscript's archives of Lua test suites and
// projects (internal/luasuites).
var archivesDir = filepath.Join("..", "..", "..", "internal", "luasuites", "testdata", "archives")

// archiveFiles returns the Lua files of the luasuites archives.
func archiveFiles(tb testing.TB) map[string][]byte {
	tb.Helper()
	paths, _ := filepath.Glob(filepath.Join(archivesDir, "*.tar.gz"))
	if len(paths) == 0 {
		tb.Skip("no archives in", archivesDir)
	}
	files := map[string][]byte{}
	for _, p := range paths {
		if err := readTarGz(p, files); err != nil {
			tb.Fatal(err)
		}
	}
	return files
}

func readTarGz(path string, files map[string][]byte) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || !strings.HasSuffix(hdr.Name, ".lua") && !strings.HasSuffix(hdr.Name, ".t") {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		files[filepath.Base(path)+"/"+hdr.Name] = b
	}
}

// corpus returns the sources of the fork's testdata (the compatibility
// scripts among them) and of the archives.
func corpus(tb testing.TB) map[string][]byte {
	tb.Helper()
	files := map[string][]byte{}
	for name, b := range luaFiles(tb, filepath.Join("..", "testdata")) {
		files["testdata/"+strings.TrimPrefix(filepath.ToSlash(name), "../testdata/")] = b
	}
	for name, b := range archiveFiles(tb) {
		files[name] = b
	}
	return files
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
