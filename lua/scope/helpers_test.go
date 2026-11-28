package scope_test

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

var dialects = []scope.Dialect{scope.Lua51, scope.LuaJIT}

func parseDialect(d scope.Dialect) parse.Dialect {
	if d == scope.LuaJIT {
		return parse.LuaJIT
	}
	return parse.Lua51
}

// mustResolve parses and resolves src.
func mustResolve(t testing.TB, src string, d scope.Dialect) (*ast.Chunk, *scope.Info) {
	t.Helper()
	chunk, err := parse.ParseChunk([]byte(src), "test", parse.Options{Dialect: parseDialect(d)})
	if err != nil {
		t.Fatalf("%v: parse %q: %v", d, src, err)
	}
	info, err := scope.Resolve(chunk, d)
	if err != nil {
		t.Fatalf("%v: resolve %q: %v", d, src, err)
	}
	return chunk, info
}

// refs lists the occurrences of names in source order as
// "x:read->global y:write->local#0", without positions.
func refs(info *scope.Info, chunk *ast.Chunk) string {
	var parts []string
	for i := range info.Refs {
		r := &info.Refs[i]
		to := "global"
		if r.Var != nil {
			to = fmt.Sprintf("%s#%d", r.Var.Kind, declOrdinal(info, r.Var))
		}
		parts = append(parts, fmt.Sprintf("%s:%s->%s", r.Name(), r.Kind, to))
	}
	return strings.Join(parts, " ")
}

// declOrdinal is the index of the variable among the variables of its name.
func declOrdinal(info *scope.Info, v *scope.Var) int {
	n := 0
	for _, u := range info.Vars {
		if u.Name == v.Name {
			if u == v {
				return n
			}
			n++
		}
	}
	return -1
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
var archivesDir = filepath.Join("..", "..", "internal", "luasuites", "testdata", "archives")

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

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
