package globals_test

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
	"github.com/tarantool-contrib/go-moonscript/luascope"
	"github.com/tarantool-contrib/go-moonscript/luascope/globals"
)

// The corpus is the Lua files of the archives of internal/luasuites, the
// LuaJIT results are those of the oracle tests of package parser.
var (
	archivesDir = filepath.Join("..", "..", "internal", "luasuites", "testdata", "archives")
	goldenDir   = filepath.Join("..", "parser", "testdata", "golden")
)

// analyze returns the usage of a chunk parsed in the default dialect.
func analyze(t *testing.T, src string) *globals.Usage {
	t.Helper()

	usage, err := globals.Analyze([]byte(src), "test", luascope.Options{})
	require.NoError(t, err, src)

	return usage
}

func readArchive(tb testing.TB, suite string) map[string][]byte {
	tb.Helper()

	f, err := os.Open(filepath.Join(archivesDir, suite+".tar.gz"))
	require.NoError(tb, err)

	defer f.Close()

	zr, err := gzip.NewReader(f)
	require.NoError(tb, err)

	files := map[string][]byte{}
	tr := tar.NewReader(zr)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files
		}

		require.NoError(tb, err)

		if hdr.Typeflag == tar.TypeReg {
			files[suite+"/"+hdr.Name], err = io.ReadAll(tr)
			require.NoError(tb, err)
		}
	}
}

// readCounts reads the number of GGET and GSET instructions per name from
// a golden file: "GGET name line..." and "GSET name count" lines.
func readCounts(t *testing.T, suite string) map[string]map[string]int {
	t.Helper()

	f, err := os.Open(filepath.Join(goldenDir, suite+".golden.gz"))
	require.NoError(t, err)

	defer f.Close()

	zr, err := gzip.NewReader(f)
	require.NoError(t, err)

	counts := map[string]map[string]int{}

	var file string

	sc := bufio.NewScanner(zr)
	sc.Buffer(nil, 1<<20)

	for sc.Scan() {
		line := sc.Text()

		switch fields := strings.Fields(line); {
		case strings.HasPrefix(line, "== "):
			file = line[3:]
		case line == "ok":
			counts[file] = map[string]int{}
		case fields[0] == "GGET":
			counts[file]["GGET "+fields[1]] += len(fields) - 2
		case fields[0] == "GSET":
			var n int

			_, err := fmt.Sscan(fields[2], &n)
			require.NoError(t, err)

			counts[file]["GSET "+fields[1]] += n
		}
	}

	require.NoError(t, sc.Err())

	return counts
}

// escapeName escapes a name like the golden files do.
func escapeName(s string) string {
	var b strings.Builder

	for i := range len(s) {
		if c := s[i]; c < 32 || c == 127 || c == '\\' || c >= 128 {
			fmt.Fprintf(&b, "\\%03d", c)
		} else {
			b.WriteByte(c)
		}
	}

	return b.String()
}

// TestAnalyzeCorpus checks that the accesses match the GGET and GSET
// instructions LuaJIT emits for the corpus: a write of a global name is a
// GSET, any other access is a GGET of the root name.
func TestAnalyzeCorpus(t *testing.T) {
	for _, suite := range []string{"lua5.1-tests", "luajit-tests", "tarantool", "vshard", "crud"} {
		t.Run(suite, func(t *testing.T) {
			files, golden := readArchive(t, suite), readCounts(t, suite)
			require.NotEmpty(t, golden)

			names := make([]string, 0, len(golden))
			for name := range golden {
				names = append(names, name)
			}

			slices.Sort(names)

			for _, name := range names {
				usage, err := globals.Analyze(files[name], "m", luascope.Options{})
				require.NoError(t, err, name)

				got := map[string]int{}

				for _, a := range usage.Accesses {
					op := "GGET "
					if a.Write && len(a.Path) == 1 {
						op = "GSET "
					}

					got[op+escapeName(a.Path[0].Name)]++
				}

				require.Equal(t, golden[name], got, name)
			}
		})
	}
}

func TestAnalyzeRequires(t *testing.T) {
	tests := []struct {
		src      string
		requires []string // Module names, "?" for a non-constant one.
		values   int      // Uses of require as a value.
	}{
		{"require('log')", []string{"log"}, 0},
		{"require 'log' require [[json]]", []string{"log", "json"}, 0},
		{"require(('cr') .. 'ud')", []string{"crud"}, 0},
		{"require(name)", []string{"?"}, 0},
		{"require()", []string{"?"}, 0},
		{"pcall(require, 'log')", []string{"log"}, 0},
		{"pcall((require), 'log', 'ignored')", []string{"log"}, 0},
		{"pcall(require, name)", []string{"?"}, 0},
		{"pcall(require)", []string{"?"}, 0},
		{"xpcall(require, handler, 'log')", []string{"log"}, 0},
		{"xpcall(require, 'log')", []string{"?"}, 0},
		{"local pcall = pcall pcall(require, 'log')", nil, 1},
		{"local require = require require('log')", nil, 1},
		{"local r = require", nil, 1},
		{"f(require, 'log')", nil, 1},
		{"pcall(f, require, 'log')", nil, 1},
		{"require.x = 1", nil, 0},
		// _G is the table of globals, so its constant fields reach require.
		{"_G.require('log')", []string{"log"}, 0},
		{"_G['require'] 'log'", []string{"log"}, 0},
		{"(_G)[('require')]('log')", []string{"log"}, 0},
		{"_G._G.require('log')", []string{"log"}, 0},
		{"_G.require(name)", []string{"?"}, 0},
		{"pcall(_G.require, 'log')", []string{"log"}, 0},
		{"xpcall(_G['require'], handler, 'log')", []string{"log"}, 0},
		{"local r = _G.require", nil, 1},
		{"local r = _G._G.require", nil, 1},
		{"local x = _G.require.x", nil, 1},
		{"f(_G.require, 'log')", nil, 1},
		{"_G:require('log')", nil, 1},
		{"_G.require.x = 1", nil, 0},
		{"local _G = _G _G.require('log')", nil, 0},
		{"_G.x.require('log')", nil, 0},
		{"_G.require._G('log')", nil, 1},
		{"local _ = _G._G", nil, 0},
	}

	for _, tt := range tests {
		usage := analyze(t, tt.src)

		var got []string

		for _, r := range usage.Requires {
			if r.Dynamic {
				got = append(got, "?")
			} else {
				got = append(got, r.Module)
			}
		}

		require.Equal(t, tt.requires, got, tt.src)
		require.Len(t, usage.RequireValues, tt.values, tt.src)
	}
}

// accessStrings formats the accesses as "line:col read|write path".
func accessStrings(usage *globals.Usage) []string {
	var got []string

	for _, a := range usage.Accesses {
		kind := "read"
		if a.Write {
			kind = "write"
		}

		got = append(got, fmt.Sprintf("%s %s %s", a.Pos, kind, a.Path))
	}

	return got
}

// TestAnalyzePaths checks the complete path, the kind and the position of
// every access.
func TestAnalyzePaths(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"box.schema.space.create()", []string{"1:1 read box.schema.space.create"}},
		// A string constant in parentheses is a constant key: LuaJIT emits
		// TGETS for it, the same as for box.schema.
		{`box[("schema")].space.create()`, []string{"1:1 read box.schema.space.create"}},
		{`box[(("schema"))].space["create"]()`, []string{"1:1 read box.schema.space.create"}},
		{`box[("space")][("users")]:select()`, []string{"1:1 read box.space.users.select"}},
		{`box[("cfg")] = {}`, []string{"1:1 write box.cfg"}},
		// A concatenation is not folded: LuaJIT emits CAT and TGETV.
		{`local _ = box["sch" .. "ema"]`, []string{"1:11 read box[?]"}},
		{"box.space.users:select()", []string{"1:1 read box.space.users.select"}},
		{"box.space[name]:replace()", []string{"1:1 read box.space[?].replace", "1:11 read name"}},
		{"(box).cfg {}", []string{"1:2 read box.cfg"}},
		{"(box.space).x:y()", []string{"1:2 read box.space.x.y"}},
		{`local _ = t["my key"][1].x`, []string{`1:11 read t["my key"][?].x`}},
		{"x = y", []string{"1:1 write x", "1:5 read y"}},
		{"box.cfg.x = 1", []string{"1:1 write box.cfg.x"}},
		{"function a.b:m() end", []string{"1:10 write a.b.m"}},
		{"function f() end", []string{"1:10 write f"}},
		{"local t = {} t.x[k].y = g", []string{"1:18 read k", "1:25 read g"}},
		{"local t = {} t:m(a.b)", []string{"1:18 read a.b"}},
		{"f().x[k].y = 1", []string{"1:1 read f", "1:7 read k"}},
		{"f(a)[b.c]:m(d)", []string{"1:1 read f", "1:3 read a", "1:6 read b.c", "1:13 read d"}},
		{"t[u[v[w]]] = 1", []string{
			"1:1 write t[?]", "1:3 read u[?]", "1:5 read v[?]", "1:7 read w",
		}},
		{"require('log')", []string{"1:1 read require"}},
		{"_G._G['require'](m)", []string{"1:1 read _G._G.require", "1:18 read m"}},
		{"pcall((_G).require, 'log')", []string{"1:1 read pcall", "1:8 read _G.require"}},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, accessStrings(analyze(t, tt.src)), tt.src)
	}
}

// TestAnalyzeLongChain checks that a chain of fields is analyzed once, from
// its outermost field. Analyzing it again from every inner field takes time
// and memory quadratic in its length. The test counts allocations, which do
// not depend on the speed of the machine: a single pass allocates while the
// path grows, a logarithmic number of times, and each extra pass over an
// inner field allocates its own path.
func TestAnalyzeLongChain(t *testing.T) {
	const fields = 10000

	for _, root := range []string{"local t = {} return t", "return f()", "return t"} {
		src := root + strings.Repeat(".x", fields)

		chunk, info, err := luascope.Parse([]byte(src), "test", luascope.Options{})
		require.NoError(t, err, root)

		allocs := testing.AllocsPerRun(1, func() { globals.AnalyzeChunk(chunk, info) })
		require.Less(t, allocs, 100.0, root)
	}
}

// TestAnalyzeSpans checks the spans and the positions of the accesses,
// requires and uses of require as a value: those of the name each starts
// with, the lines counted as the lexer counts them.
func TestAnalyzeSpans(t *testing.T) {
	src := "x = 1\r\n\tbox.cfg{}\n\rlocal r = _G.require\r" +
		"local m = (require)'m' --[[\n]] y.z = 1"
	usage := analyze(t, src)

	var got []string

	for _, a := range usage.Accesses {
		got = append(got, fmt.Sprintf("%s %v %q", a.Pos, a.Span, src[a.Span.Start:a.Span.End]))
	}

	require.Equal(t, []string{
		`1:1 0-1 "x"`, `2:2 8-11 "box"`, `3:11 29-31 "_G"`, `4:12 51-58 "require"`,
		`5:4 71-72 "y"`,
	}, got)

	require.Len(t, usage.Requires, 1)
	assert.Equal(t, ast.Span{Start: 51, End: 58}, usage.Requires[0].Span)
	assert.Equal(t, ast.Pos{Offset: 51, Line: 4, Column: 12}, usage.Requires[0].Pos)

	require.Len(t, usage.RequireValues, 1)
	assert.Equal(t, usage.Accesses[2], usage.RequireValues[0])
}

// TestAnalyzeChunkWithoutPositions checks a tree built by other code: it
// has no spans and no lines, so the accesses have no positions.
func TestAnalyzeChunkWithoutPositions(t *testing.T) {
	stmts := []ast.Stmt{&ast.FuncCallStmt{Expr: &ast.FuncCallExpr{
		Func: &ast.AttrGetExpr{
			Object: &ast.IdentExpr{Value: "box"},
			Key:    &ast.StringExpr{Value: "cfg"},
		},
	}}}

	info, err := scope.ResolveStmts(stmts, scope.LuaJIT)
	require.NoError(t, err)

	usage := globals.AnalyzeChunk(&ast.Chunk{Stmts: stmts}, info)
	require.Len(t, usage.Accesses, 1)
	assert.Equal(t, "box.cfg", usage.Accesses[0].Path.String())
	assert.Equal(t, ast.Span{}, usage.Accesses[0].Span)
	assert.False(t, usage.Accesses[0].Pos.IsValid())
}

// TestAnalyzeChunkNilNodes checks a tree built by other code with nil
// nodes, which are skipped as package scope skips them.
func TestAnalyzeChunkNilNodes(t *testing.T) {
	call := func(f ast.Expr, args ...ast.Expr) ast.Stmt {
		return &ast.FuncCallStmt{Expr: &ast.FuncCallExpr{Func: f, Args: args}}
	}
	req := &ast.IdentExpr{Value: "require"}
	nilObject := &ast.AttrGetExpr{
		Object: (*ast.AttrGetExpr)(nil),
		Key:    &ast.StringExpr{Value: "require"},
	}
	stmts := []ast.Stmt{
		nil, (*ast.AssignStmt)(nil), (*ast.IfStmt)(nil), (*ast.FuncDefStmt)(nil),
		call(req, (*ast.StringExpr)(nil)),
		call(req, (*ast.StringConcatOpExpr)(nil)),
		call(nilObject),
		call(&ast.AttrGetExpr{Object: &ast.IdentExpr{Value: "_G"}, Key: (*ast.StringExpr)(nil)}),
		&ast.ReturnStmt{Exprs: []ast.Expr{nil, (*ast.IdentExpr)(nil), (*ast.TableExpr)(nil)}},
	}

	info, err := scope.ResolveStmts(stmts, scope.LuaJIT)
	require.NoError(t, err)

	usage := globals.AnalyzeChunk(&ast.Chunk{Stmts: stmts}, info)
	assert.Equal(t, []string{"- read require", "- read require", "- read _G[?]"},
		accessStrings(usage))
	assert.Len(t, usage.Requires, 2)
}

func TestAnalyzeSyntaxError(t *testing.T) {
	_, err := globals.Analyze([]byte("x ="), "@m.lua", luascope.Options{})
	require.EqualError(t, err, "m.lua:1: unexpected symbol near '<eof>'")
}
