package format_test

// The checks of the formatter and the printer over a corpus of sources:
// for a source S that parses and compiles, in a dialect,
//
//   - Source(S) parses and compiles to the code S compiles to, but for the
//     lines (protoDiff);
//   - Source(Source(S)) is Source(S);
//   - the comments of Source(S) are those of S, as written, in order;
//   - the string and numeral tokens of Source(S) are those of S (a
//     multiset: the order of the operands is kept, but a check of order
//     would fail on nothing else);
//   - Node(the tree of S) parses and compiles to the code of S, and
//     printing its tree again gives the same text.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	lua "github.com/tarantool-contrib/go-moonscript/lua"
	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/format"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
)

var dialects = []parse.Dialect{parse.Lua51, parse.LuaJIT}

func dialectName(d parse.Dialect) string {
	if d == parse.LuaJIT {
		return "LuaJIT"
	}
	return "Lua51"
}

func luaDialect(d parse.Dialect) lua.Dialect {
	if d == parse.LuaJIT {
		return lua.DialectLuaJIT
	}
	return lua.DialectLua51
}

// compile parses (as Source does) and compiles src.
func compile(src []byte, d parse.Dialect) (*lua.FunctionProto, error) {
	chunk, err := parse.ParseChunk(src, "check", parse.Options{Dialect: d, Shebang: true})
	if err != nil {
		return nil, err
	}
	return lua.CompileAST(chunk.Stmts, "=check", luaDialect(d))
}

// luaCompile compiles a tree.
func luaCompile(stmts []ast.Stmt, d parse.Dialect) (*lua.FunctionProto, error) {
	return lua.CompileAST(stmts, "=check", luaDialect(d))
}

// protoDiff compares two function prototypes but for their lines: the
// code, the constants, the nested prototypes, the counts of upvalues,
// parameters and registers, the vararg flag and the debug names of the
// locals (with their pc ranges), the calls and the upvalues. It returns
// the first difference, "" if none.
func protoDiff(a, b *lua.FunctionProto, where string) string {
	switch {
	case a.NumUpvalues != b.NumUpvalues:
		return fmt.Sprintf("%s: upvalues %d, %d", where, a.NumUpvalues, b.NumUpvalues)
	case a.NumParameters != b.NumParameters:
		return fmt.Sprintf("%s: parameters %d, %d", where, a.NumParameters, b.NumParameters)
	case a.IsVarArg != b.IsVarArg:
		return fmt.Sprintf("%s: vararg %d, %d", where, a.IsVarArg, b.IsVarArg)
	case a.NumUsedRegisters != b.NumUsedRegisters:
		return fmt.Sprintf("%s: registers %d, %d", where, a.NumUsedRegisters, b.NumUsedRegisters)
	case len(a.Code) != len(b.Code):
		return fmt.Sprintf("%s: %d instructions, %d", where, len(a.Code), len(b.Code))
	case len(a.Constants) != len(b.Constants):
		return fmt.Sprintf("%s: %d constants, %d", where, len(a.Constants), len(b.Constants))
	case len(a.FunctionPrototypes) != len(b.FunctionPrototypes):
		return fmt.Sprintf("%s: %d functions, %d", where, len(a.FunctionPrototypes), len(b.FunctionPrototypes))
	case len(a.DbgLocals) != len(b.DbgLocals):
		return fmt.Sprintf("%s: %d locals, %d", where, len(a.DbgLocals), len(b.DbgLocals))
	case len(a.DbgCalls) != len(b.DbgCalls):
		return fmt.Sprintf("%s: %d calls, %d", where, len(a.DbgCalls), len(b.DbgCalls))
	case strings.Join(a.DbgUpvalues, ",") != strings.Join(b.DbgUpvalues, ","):
		return fmt.Sprintf("%s: upvalues %v, %v", where, a.DbgUpvalues, b.DbgUpvalues)
	}
	for i := range a.Code {
		if a.Code[i] != b.Code[i] {
			return fmt.Sprintf("%s: instruction %d: %08x, %08x", where, i, a.Code[i], b.Code[i])
		}
	}
	for i := range a.Constants {
		if !sameConstant(a.Constants[i], b.Constants[i]) {
			return fmt.Sprintf("%s: constant %d: %v, %v", where, i, a.Constants[i], b.Constants[i])
		}
	}
	for i, l := range a.DbgLocals {
		m := b.DbgLocals[i]
		if l.Name != m.Name || l.StartPc != m.StartPc || l.EndPc != m.EndPc {
			return fmt.Sprintf("%s: local %d: %s %d-%d, %s %d-%d", where, i, l.Name, l.StartPc, l.EndPc, m.Name, m.StartPc, m.EndPc)
		}
	}
	for i, c := range a.DbgCalls {
		if c.Name != b.DbgCalls[i].Name || c.Pc != b.DbgCalls[i].Pc {
			return fmt.Sprintf("%s: call %d: %s@%d, %s@%d", where, i, c.Name, c.Pc, b.DbgCalls[i].Name, b.DbgCalls[i].Pc)
		}
	}
	for i := range a.FunctionPrototypes {
		if d := protoDiff(a.FunctionPrototypes[i], b.FunctionPrototypes[i], fmt.Sprintf("%s/%d", where, i)); d != "" {
			return d
		}
	}
	return ""
}

func sameConstant(a, b lua.LValue) bool {
	if x, ok := a.(lua.LNumber); ok {
		y, ok := b.(lua.LNumber)
		if !ok {
			return false
		}
		if math.IsNaN(float64(x)) {
			return math.IsNaN(float64(y))
		}
		return math.Float64bits(float64(x)) == math.Float64bits(float64(y))
	}
	if x, ok := a.(lua.LString); ok {
		y, ok := b.(lua.LString)
		return ok && x == y
	}
	return a.Type() == b.Type() && a.String() == b.String()
}

// lexemes returns the texts of the tokens of src of the kinds.
func lexemes(src []byte, d parse.Dialect, kinds ...ast.TokenKind) []string {
	toks, err := parse.Tokenize(src, "check", parse.Options{Dialect: d, Shebang: true})
	if err != nil {
		return []string{"<error: " + err.Error() + ">"}
	}
	var out []string
	for _, t := range toks {
		for _, k := range kinds {
			if t.Kind == k {
				out = append(out, string(t.Text(src)))
			}
		}
	}
	return out
}

func sorted(list []string) []string {
	l := append([]string(nil), list...)
	sort.Strings(l)
	return l
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkStats counts what the checks compared.
type checkStats struct {
	sources, skipped, failed     int
	bytes, outBytes              int
	comments, literals, printers int
}

func (s *checkStats) add(o checkStats) {
	s.sources += o.sources
	s.skipped += o.skipped
	s.failed += o.failed
	s.bytes += o.bytes
	s.outBytes += o.outBytes
	s.comments += o.comments
	s.literals += o.literals
	s.printers += o.printers
}

// checkSource runs the checks on src; it returns the first failure, "" if
// none, and whether src was checked (it parses and compiles).
func checkSource(src []byte, opts format.Options, stats *checkStats) (failure string, checked bool) {
	d := opts.Dialect
	want, err := compile(src, d)
	if err != nil {
		return "", false
	}
	stats.bytes += len(src)
	out, err := format.Source(src, opts)
	if err != nil {
		return "format: " + err.Error(), true
	}
	stats.outBytes += len(out)
	got, err := compile(out, d)
	if err != nil {
		return fmt.Sprintf("the formatted source does not compile: %v\n%s", err, excerpt(out, err)), true
	}
	if diff := protoDiff(want, got, "main"); diff != "" {
		return "the formatted source compiles to other code: " + diff, true
	}
	again, err := format.Source(out, opts)
	if err != nil {
		return "format of the formatted source: " + err.Error(), true
	}
	if !bytes.Equal(again, out) {
		return "not idempotent:\n" + firstDiff(out, again), true
	}
	c1 := lexemes(src, d, ast.TokenComment)
	c2 := lexemes(out, d, ast.TokenComment)
	if !equalStrings(c1, c2) {
		return fmt.Sprintf("comments differ: %d, %d: %s", len(c1), len(c2), firstStringDiff(c1, c2)), true
	}
	stats.comments += len(c1)
	if opts.Quotes == format.QuotesKeep {
		l1 := sorted(lexemes(src, d, ast.TokenString, ast.TokenNumber))
		l2 := sorted(lexemes(out, d, ast.TokenString, ast.TokenNumber))
		if !equalStrings(l1, l2) {
			return fmt.Sprintf("literals differ: %d, %d: %s", len(l1), len(l2), firstStringDiff(l1, l2)), true
		}
		stats.literals += len(l1)
	}
	if f := checkPrinter(src, want, opts); f != "" {
		return f, true
	}
	stats.printers++
	return "", true
}

// checkPrinter checks Node on the tree of src, parsed without tokens.
func checkPrinter(src []byte, want *lua.FunctionProto, opts format.Options) string {
	d := opts.Dialect
	chunk, err := parse.ParseChunk(src, "check", parse.Options{Dialect: d, Shebang: true})
	if err != nil {
		return "parse: " + err.Error()
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, chunk, opts); err != nil {
		return "print: " + err.Error()
	}
	out := buf.Bytes()
	got, err := compile(out, d)
	if err != nil {
		return fmt.Sprintf("the printed tree does not compile: %v\n%s", err, excerpt(out, err))
	}
	if diff := protoDiff(want, got, "main"); diff != "" {
		return "the printed tree compiles to other code: " + diff
	}
	chunk2, err := parse.ParseChunk(out, "check", parse.Options{Dialect: d})
	if err != nil {
		return "parse of the printed tree: " + err.Error()
	}
	var buf2 bytes.Buffer
	if err := format.Node(&buf2, chunk2, opts); err != nil {
		return "print of the printed tree: " + err.Error()
	}
	if !bytes.Equal(buf2.Bytes(), out) {
		return "printing the printed tree differs:\n" + firstDiff(out, buf2.Bytes())
	}
	return ""
}

// excerpt returns the lines around the line of a parse error.
func excerpt(src []byte, err error) string {
	var pe *parse.Error
	if !errors.As(err, &pe) {
		return ""
	}
	lines := strings.Split(string(src), "\n")
	from, to := max(0, pe.Pos.Line-4), min(len(lines), pe.Pos.Line+2)
	var b strings.Builder
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, "%5d  %s\n", i+1, lines[i])
	}
	return b.String()
}

func firstDiff(a, b []byte) string {
	la := strings.Split(string(a), "\n")
	lb := strings.Split(string(b), "\n")
	for i := 0; i < len(la) || i < len(lb); i++ {
		var x, y string
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if x != y {
			from := max(0, i-3)
			var s strings.Builder
			for j := from; j < i; j++ {
				fmt.Fprintf(&s, "   %5d  %s\n", j+1, la[j])
			}
			fmt.Fprintf(&s, " - %5d  %s\n + %5d  %s\n", i+1, x, i+1, y)
			return s.String()
		}
	}
	return ""
}

func firstStringDiff(a, b []string) string {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return fmt.Sprintf("#%d %q, %q", i, x, y)
		}
	}
	return ""
}

// runChecks runs the checks over files in both dialects.
func runChecks(t *testing.T, label string, files map[string][]byte, opts format.Options) {
	t.Helper()
	for _, d := range dialects {
		var total checkStats
		o := opts
		o.Dialect = d
		for _, name := range sortedKeys(files) {
			var st checkStats
			st.sources = 1
			f, checked := checkSource(files[name], o, &st)
			if !checked {
				st.skipped = 1
			}
			if f != "" {
				st.failed = 1
				if total.failed < 10 {
					t.Errorf("%s (%s): %s", name, dialectName(d), f)
				}
			}
			total.add(st)
		}
		t.Logf("%s, %s: %d sources (%d do not compile), %d formatted (%d bytes to %d): %d fail; %d comments, %d literals, %d printed trees compared",
			label, dialectName(d), total.sources, total.skipped, total.sources-total.skipped, total.bytes, total.outBytes,
			total.failed, total.comments, total.literals, total.printers)
	}
}

/* the corpus */

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

// TestCheckTestdata runs the checks over the compatibility scripts and the
// other Lua files of the fork's testdata, and the Lua files of
// go-moonscript's testdata.
func TestCheckTestdata(t *testing.T) {
	files := luaFiles(t, filepath.Join("..", "testdata"))
	if len(files) == 0 {
		t.Fatal("no scripts")
	}
	runChecks(t, "gopher-lua testdata", files, format.Options{})
	// go-moonscript's, if the fork is in it
	if dir := filepath.Join("..", "..", "moonscript", "testdata"); isDir(dir) {
		runChecks(t, "go-moonscript testdata", luaFiles(t, dir), format.Options{})
	}
}

// TestCheckArchives runs the checks over the Lua files of the luasuites
// archives.
func TestCheckArchives(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	runChecks(t, "archives", archiveFiles(t), format.Options{})
}

// TestCheckOptions runs the checks over the compatibility scripts with the
// other options: narrow lines, tabs, quotes, trailing commas, semicolons.
func TestCheckOptions(t *testing.T) {
	files := luaFiles(t, filepath.Join("..", "testdata", "compat"))
	runChecks(t, "compat, narrow", files, format.Options{Width: 40, UseTabs: true, TrailingComma: true, KeepSemicolons: true})
	runChecks(t, "compat, quotes", files, format.Options{Quotes: format.QuotesSingle, IndentWidth: 2})
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
