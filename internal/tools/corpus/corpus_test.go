package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/ast"
	"github.com/tarantool-contrib/go-moonscript/compiler"
	"github.com/tarantool-contrib/go-moonscript/parse"
)

// goRef builds a reference result from the Go port itself, so the synthetic
// tests below do not need Docker or the corpus.
func goRef(t *testing.T, src string) Ref {
	t.Helper()
	tree, err := parse.String(src)
	if err != nil {
		return Ref{Status: "parse-error", Err: err.Error()}
	}
	r := Ref{Status: "ok", Tree: ast.Tree(tree), HasTree: true}
	code, _, cerr := compiler.Tree(tree, compiler.Options{})
	if cerr != nil {
		r.Status, r.Err = "compile-error", compiler.FormatError(cerr.Msg, cerr.Pos, src)
		return r
	}
	r.Lua, r.HasLua = code, true
	return r
}

// TestClassify checks that every kind of difference lands in its category.
func TestClassify(t *testing.T) {
	const ok = "x = 1\nf = (a) -> a + x\nprint f 2\n"
	const bad = "x = \n  ) y\n"
	const cerr = "x = 1\n.y = 2\n"

	cases := []struct {
		name, src     string
		mutate        func(*Ref)
		parse, compil string
	}{
		{"match", ok, nil, PTreeMatch, CLuaMatch},
		{"tree mismatch", ok, func(r *Ref) { r.Tree = strings.Replace(r.Tree, `"x"`, `"y"`, 1) }, PTreeMismatch, CLuaMatch},
		{"lua mismatch", ok, func(r *Ref) { r.Lua = strings.Replace(r.Lua, "a + x", "a - x", 1) }, PTreeMatch, CLuaMismatch},
		{"go rejects valid", bad, func(r *Ref) { *r = Ref{Status: "ok", Tree: "{}", HasTree: true, Lua: "", HasLua: true} }, PGoRejects, CSkip},
		{"parse error match", bad, nil, PErrMatch, CSkip},
		{"parse error mismatch", bad, func(r *Ref) { r.Err = strings.Replace(r.Err, "line 2", "line 3", 1) }, PErrMismatch, CSkip},
		{"go accepts invalid", ok, func(r *Ref) { *r = Ref{Status: "parse-error", Err: "failed to parse at line 1"} }, PGoAccepts, CSkip},
		{"compile error match", cerr, nil, PTreeMatch, CErrMatch},
		{"compile error mismatch", cerr, func(r *Ref) { r.Err += "!" }, PTreeMatch, CErrMismatch},
		{"go compile error", cerr, func(r *Ref) { r.Status, r.Lua, r.HasLua = "ok", "local x = 1", true }, PTreeMatch, CGoError},
		{"go compiles invalid", ok, func(r *Ref) { r.Status, r.Err = "compile-error", "failed to compile: nope" }, PTreeMatch, CGoAccepts},
		{"ref internal", ok, func(r *Ref) { r.Status, r.Err = "compile-internal", "failed to compile: x.lua:1: boom" }, PTreeMatch, CRefInternalDiff},
		{"no ref", ok, func(r *Ref) { *r = Ref{} }, PNoRef, CSkip},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ref := goRef(t, c.src)
			if c.mutate != nil {
				c.mutate(&ref)
			}
			res := check("id", c.src, ref, false)
			if res.Parse != c.parse || res.Compile != c.compil {
				t.Fatalf("got %s/%s, want %s/%s (buckets %q %q)", res.Parse, res.Compile, c.parse, c.compil,
					res.ParseBucket, res.CompileBucket)
			}
			if (res.Parse != PTreeMatch && res.Parse != PErrMatch && res.Parse != PNoRef && res.ParseBucket == "") ||
				(res.Compile != CLuaMatch && res.Compile != CErrMatch && res.Compile != CSkip && res.CompileBucket == "") {
				t.Errorf("missing bucket: %+v", res)
			}
		})
	}
}

func TestBuckets(t *testing.T) {
	b, _ := luaDiff("local x = 1\nreturn x", "local x = 1\nreturn y")
	if b != "names/literals differ: return x" {
		t.Errorf("luaDiff bucket %q", b)
	}
	b, _ = luaDiff("a\n  local _base_0 = { }", "a\n  local _base_0 = {}")
	if b != "spacing differs: local _base_N = { }" {
		t.Errorf("luaDiff bucket %q", b)
	}
	b, _ = treeDiff(`{[1] "assign", {{[1] "ref", "x"}}}`, `{[1] "assign", {{[2] "ref", "x"}}}`, "x = 1")
	if b != "in assign/list: position differs" {
		t.Errorf("treeDiff bucket %q", b)
	}
	b, _ = treeDiff(`{[1] "chain", {[1] "ref", "f"}, {"call", {}}}`, `{[1] "chain", {[1] "ref", "f"}, {"index", {}}}`, "f!")
	if b != `in chain: want "call" | got "index"` {
		t.Errorf("treeDiff bucket %q", b)
	}
	if got := faultFrame("boom\ngoroutine 1:\npanic({0x1, 0x2})\n\t/x/panic.go:1\ngithub.com/tarantool-contrib/go-moonscript/compiler.(*Block).value(0x1)\n\t/x/b.go:3\n"); got != "compiler.(*Block).value" {
		t.Errorf("faultFrame %q", got)
	}
	if got := normInternal("failed to compile: /usr/share/lua/5.1/moonscript/compile.lua:12: attempt to index nil\nstack"); got != "attempt to index nil" {
		t.Errorf("normInternal %q", got)
	}
}

// TestCorpus runs the whole corpus in-process. It is opt-in because it needs
// the corpus and takes a while:
//
//	MOON_CORPUS=~/.cache/go-moonscript/corpus go test ./internal/tools/corpus -run TestCorpus
//
// MOON_CORPUS_FILTER restricts it like -filter. It fails on every file that
// does not match the reference.
func TestCorpus(t *testing.T) {
	dir := os.Getenv("MOON_CORPUS")
	if dir == "" {
		t.Skip("MOON_CORPUS not set")
	}
	files, err := readManifest(dir)
	if err != nil {
		t.Skip(err)
	}
	filter := os.Getenv("MOON_CORPUS_FILTER")
	var res []Result
	byID := map[string]*Occurrence{}
	for _, f := range files {
		if !f.matches(filter) {
			continue
		}
		byID[f.ID] = f
		src, err := os.ReadFile(filepath.Join(dir, "files", f.ID+".moon"))
		if err != nil {
			t.Fatal(err)
		}
		r, _ := checkTimeout(f.ID, string(src), loadRef(dir, f.ID), false, *flagTimeout)
		res = append(res, r)
	}
	t.Logf("\n%s", summarize(res).text(len(files)-len(res), false))
	buckets := bucketize(res, byID)
	for _, cat := range categoryOrder {
		for _, b := range buckets[cat] {
			t.Errorf("%s: %d files: %s (e.g. %s %s)", cat, len(b.files), b.key, b.files[0].ID, b.files[0].Name())
		}
	}
}

// TestClassifyLint checks the lint mode categories with Go-made references.
func TestClassifyLint(t *testing.T) {
	const src = "a, b = 1, 2\nprint b, missing\n"
	ref := goLint("x", src)
	if ref.Status != "lint" || !strings.Contains(ref.Compact, "x.moon:2:9: accessing global `missing`") {
		t.Fatalf("unexpected lint result %+v", ref)
	}
	if o, _ := classifyLint(ref, ref); o != LMatch {
		t.Errorf("same output: %s", o)
	}
	changed := ref
	changed.Compact = strings.Replace(changed.Compact, "2:9", "2:10", 1)
	if o, d := classifyLint(changed, ref); o != LMismatch || d == "" {
		t.Errorf("changed output: %s %q", o, d)
	}
	if o, _ := classifyLint(lintOut{Status: "clean"}, ref); o != LStatus {
		t.Errorf("status: %s", o)
	}
	crash := goLint("x", "x = 1\n.foo!\n")
	if crash.Status != "crash-table" {
		t.Fatalf("crash: %+v", crash)
	}
	refCrash := lintOut{Status: "crash-table", Err: "user-error\nShort-dot syntax must be called within a with block\n" +
		strings.SplitN(crash.Err, "\n", 3)[2]}
	if o, d := classifyLint(refCrash, crash); o != LCrashMatch {
		t.Errorf("crash: %s %s", o, d)
	}
	if o, _ := classifyLint(lintOut{Status: "ref-timeout"}, ref); o != LNoRef {
		t.Errorf("no ref: %s", o)
	}
}
