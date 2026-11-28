package main

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// mutantSrc is the input of TestMutantsStable.
const mutantSrc = "class Foo\n  new: (@x) =>\n\n  get: => @x\n\nf = Foo 1\nprint f\\get!, \"done\"\n"

// TestMutantsStable pins the mutant generator: the expected results of the
// committed corpus manifest hold the reference outputs of its mutants, so
// it must generate the same mutants as when they were made.
func TestMutantsStable(t *testing.T) {
	var got []string
	for _, id := range []string{"0123456789abcdef", "07219cd9561b41ce", "ffffffffffffffff"} {
		for _, m := range makeMutants(id, []byte(mutantSrc), 6) {
			got = append(got, fmt.Sprintf("%s %s%s %s", id, m.kind, m.detail, digest(string(m.data))))
		}
	}
	want := []string{
		`0123456789abcdef indent-line@line1 3f0b75cb619dce92`,
		`0123456789abcdef insert-char@byte60"!" 269b623bf49f9100`,
		`0123456789abcdef cut-byte@byte39 12df8052b7c087fd`,
		`0123456789abcdef cut-line@line3 9c54dcb7f7977670`,
		`0123456789abcdef drop-line@line2 ed6b5b0317569f88`,
		`07219cd9561b41ce insert-char@byte46"'" 68b565b6af6304c2`,
		`07219cd9561b41ce cut-byte@byte14 1920dc33d961926d`,
		`07219cd9561b41ce cut-line@line2 2d75892e8db0fdc7`,
		`07219cd9561b41ce drop-line@line6 ab04dfb48b7947fc`,
		`07219cd9561b41ce indent-line@line2 15753585fc48aba0`,
		`07219cd9561b41ce dedent-line@line2 2b383f4566e16c87`,
		`ffffffffffffffff indent-line@line2 15753585fc48aba0`,
		`ffffffffffffffff insert-char@byte10"|" 3acecf30ee9d297d`,
		`ffffffffffffffff cut-byte@byte2 d192375885ec7d50`,
		`ffffffffffffffff cut-line@line4 12df8052b7c087fd`,
		`ffffffffffffffff drop-line@line2 ed6b5b0317569f88`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("mutants changed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestMutantRand checks mutantRand against the math/rand/v2 stream it was
// taken from (the one that made the corpus mutants). Should a Go release
// change that stream, mutantRand stays as it is (TestMutantsStable) and
// this test is to be dropped.
func TestMutantRand(t *testing.T) {
	for seed := range uint64(50) {
		s := seed * 0x9e3779b97f4a7c15
		std := rand.New(rand.NewPCG(s, 0x6d6f6f6e))
		ours := &mutantRand{hi: s, lo: 0x6d6f6f6e}
		for i := range 200 {
			n := []int{1, 2, 3, 7, 64, 1000, 4 << 20, 1<<40 + 3}[i%8]
			if a, b := std.IntN(n), ours.IntN(n); a != b {
				t.Fatalf("seed %d, call %d, IntN(%d): math/rand/v2 %d, mutantRand %d", seed, i, n, a, b)
			}
		}
	}
}

// TestDigestRef checks the digest form of the reference results: the same
// outcomes as with the texts.
func TestDigestRef(t *testing.T) {
	cases := []struct {
		name, src     string
		mutate        func(*Ref)
		parse, compil string
	}{
		{"ok", "x = 1\nf = (a) -> a + x\nprint f 2\n", nil, PTreeMatch, CLuaMatch},
		{"parse error", "x = \n  ) y\n", nil, PErrMatch, CSkip},
		{"compile error", "x = 1\n.y = 2\n", nil, PTreeMatch, CErrMatch},
		{"tree", "x = 1\n", func(r *Ref) { r.Tree += " " }, PTreeMismatch, CLuaMatch},
		{"lua", "x = 1\n", func(r *Ref) { r.Lua += " " }, PTreeMatch, CLuaMismatch},
		{"parse error text", "x = \n  ) y\n", func(r *Ref) { r.Err += " " }, PErrMismatch, CSkip},
		{"compile error text", "x = 1\n.y = 2\n", func(r *Ref) { r.Err += " " }, PTreeMatch, CErrMismatch},
		{"go accepts", "x = 1\n", func(r *Ref) { *r = Ref{Status: "parse-error", Err: "nope"} }, PGoAccepts, CSkip},
		{"internal", "x = 1\n", func(r *Ref) {
			r.Status, r.Err = "compile-internal", "failed to compile: /x/compile.lua:3: boom\nstack traceback:\n..."
		}, PTreeMatch, CRefInternalDiff},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ref := goRef(t, c.src)
			if c.mutate != nil {
				c.mutate(&ref)
			}
			hashed, err := parseRefEntry(refEntry(ref))
			if err != nil {
				t.Fatal(err)
			}
			res := check("id", c.src, hashed, false)
			if res.Parse != c.parse || res.Compile != c.compil {
				t.Fatalf("got %s/%s, want %s/%s (%s)", res.Parse, res.Compile, c.parse, c.compil, refEntry(ref))
			}
			if plain := check("id", c.src, ref, false); plain.Parse != res.Parse || plain.Compile != res.Compile {
				t.Errorf("texts: %s/%s, digests: %s/%s", plain.Parse, plain.Compile, res.Parse, res.Compile)
			}
		})
	}
	for _, e := range []string{"", "o:1", "p:1:2", "x:1"} {
		if _, err := parseRefEntry(e); err == nil {
			t.Errorf("parseRefEntry(%q): no error", e)
		}
	}
	if r, err := parseRefEntry("ref-timeout"); err != nil || r.Status != "ref-timeout" || check("id", "x", r, false).Parse != PNoRef {
		t.Errorf("ref-timeout: %+v %v", r, err)
	}
}

// TestDigestLint checks the digest form of the reference lint results.
func TestDigestLint(t *testing.T) {
	for _, src := range []string{"a, b = 1, 2\nprint b, missing\n", "x = 1\nprint x\n", "x = \n  ) y\n", "x = 1\n.foo!\n"} {
		got := goLint("x", src)
		ref, err := parseLintEntry(lintEntry(got))
		if err != nil {
			t.Fatal(err)
		}
		plain, _ := classifyLint(got, got)
		if o, d := classifyLint(ref, got); o != plain {
			t.Errorf("%q (%s): digest %s %s, texts %s", src, got.Status, o, d, plain)
		}
		other := got
		other.Compact, other.Err = other.Compact+" ", other.Err+" "
		if o, _ := classifyLint(ref, other); got.Status != "clean" && (o == LMatch || o == LCrashMatch) {
			t.Errorf("%q (%s): changed output matches", src, got.Status)
		}
	}
	// crash-string: the reference message has a Lua location
	ref, _ := parseLintEntry(lintEntry(lintOut{Status: "crash-string",
		Err: "...usr/local/share/lua/5.1/moonscript/compile/value.lua:228: Unknown chain action: slice"}))
	if o, d := classifyLint(ref, lintOut{Status: "crash-string", Err: "Unknown chain action: slice"}); o != LCrashMatch {
		t.Errorf("crash-string: %s %s", o, d)
	}
	for _, e := range []string{"", "c:1", "l"} {
		if _, err := parseLintEntry(e); err == nil {
			t.Errorf("parseLintEntry(%q): no error", e)
		}
	}
}

func TestGitBlobID(t *testing.T) {
	// git hash-object of an empty file and of "hello\n"
	if got := gitBlobID(nil); got != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" {
		t.Errorf("empty: %s", got)
	}
	if got := gitBlobID([]byte("hello\n")); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Errorf("hello: %s", got)
	}
}

func TestGreedyCover(t *testing.T) {
	byRepo := map[string]map[string]string{
		"s\x00a/fork1": {"1": "x", "2": "y"},
		"s\x00a/orig":  {"1": "x", "2": "y", "3": "z"},
		"s\x00b/other": {"3": "z", "4": "w"},
		"s\x00c/dup":   {"4": "w"},
	}
	res := greedyCover(byRepo)
	if want := []string{"s\x00a/orig", "s\x00b/other"}; !slices.Equal(res.order, want) {
		t.Fatalf("cover %q, want %q", res.order, want)
	}
	if !slices.Equal(res.files["s\x00b/other"], []string{"4"}) {
		t.Errorf("files of b/other: %v", res.files["s\x00b/other"])
	}
}

// TestPinnedCorpus checks the committed manifest and expected results:
// formats, and an entry for every file.
func TestPinnedCorpus(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "moonscript", "testdata", "corpus")
	repos, err := readPinManifest(filepath.Join(dir, "manifest.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	exp, err := readExpectedTSV(filepath.Join(dir, "expected.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	hex40, hex64 := regexp.MustCompile(`^[0-9a-f]{40}$`), regexp.MustCompile(`^[0-9a-f]{64}$`)
	seen := map[string]bool{}
	nfiles := 0
	for _, r := range repos {
		if !slices.Contains(segmentOrder, r.Segment) || !hex40.MatchString(r.Commit) ||
			!regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(r.Repo) || len(r.Files) == 0 {
			t.Fatalf("bad repository %+v", *r)
		}
		for _, f := range r.Files {
			nfiles++
			id := f.id()
			if !hex64.MatchString(f.SHA) || !hex40.MatchString(f.Blob) || f.Path == "" || seen[id] {
				t.Fatalf("%s: bad or duplicate file %+v", r.Repo, f)
			}
			seen[id] = true
			e := exp[id]
			if e == nil {
				t.Fatalf("%s:%s: no expected results", r.Repo, f.Path)
			}
			for i, field := range e {
				if i == 1 {
					if _, err := parseLintEntry(field); err != nil || (field == "-") != (r.Segment != "moonscript") {
						t.Fatalf("%s: lint entry %q: %v", id, field, err)
					}
				} else if _, err := parseRefEntry(field); err != nil {
					t.Fatalf("%s: %v", id, err)
				}
			}
			if len(e) > 2+*flagMutants || (r.Segment != "moonscript" && len(e) > 2) {
				t.Fatalf("%s: %d mutant entries", id, len(e)-2)
			}
		}
	}
	if len(exp) != nfiles {
		t.Errorf("expected.tsv has %d files, manifest.tsv %d", len(exp), nfiles)
	}
	if _, err := os.Stat(filepath.Join(dir, "expected.tsv")); err != nil {
		t.Fatal(err)
	}
}
