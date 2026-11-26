package moonscript

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocs compiles every MoonScript code block of the upstream v0.8.0
// documentation (docs/*.md: ```moon, ```moononly and ```moonret fences) and
// compares with the reference compiler.
//
// Layout: testdata/docs/<doc>_<n>.moon is the block, <doc>_<n>.lua the exact
// moonscript.base.to_lua output of the reference and <doc>_<n>.err the exact
// error message when the reference rejects the block (none currently do:
// even the fragments compile standalone). testdata/docs/MANIFEST.tsv records
// the doc and line each block comes from. Regenerate with
// scripts/gen-docs-golden.sh (needs Docker and the moonref:0.8.0 image).
//
//	go test -run Docs .
//	go test -run 'Docs/reference_04' .
func TestDocs(t *testing.T) {
	sources := docsManifest(t)
	inputs, _ := filepath.Glob("testdata/docs/*.moon")
	if len(inputs) == 0 {
		t.Fatal("no inputs")
	}
	if len(inputs) != len(sources) {
		t.Errorf("MANIFEST.tsv lists %d blocks, found %d inputs", len(sources), len(inputs))
	}
	for _, in := range inputs {
		base := strings.TrimSuffix(in, ".moon")
		name := filepath.Base(base)
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			code, _, cerr := ToLua(string(src), Options{})
			if want, err := os.ReadFile(base + ".err"); err == nil {
				if cerr == nil {
					t.Fatalf("%s: expected error %q, compiled to\n%s", sources[name], want, code)
				}
				if cerr.Error() != string(want) {
					t.Errorf("%s: error mismatch\n--- want ---\n%s\n--- got ---\n%s", sources[name], want, cerr)
				}
				return
			}
			want, err := os.ReadFile(base + ".lua")
			if err != nil {
				t.Fatal(err)
			}
			if cerr != nil {
				t.Fatalf("%s: %v", sources[name], cerr)
			}
			if code != string(want) {
				t.Errorf("%s: output mismatch\n%s", sources[name], firstDiff(string(want), code))
			}
		})
	}
}

// docsManifest maps a block name to its "docs/<file>.md:<line>" origin.
func docsManifest(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open("testdata/docs/MANIFEST.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			t.Fatalf("bad manifest line %q", line)
		}
		out[fields[0]] = fields[1]
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
