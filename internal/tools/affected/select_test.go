package main

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeGraph is the go list output of a module m in /r:
//
//	lua/parse         (lua/parse.go)
//	lua/flow          imports lua/parse; reads lua/testdata (DataDeps)
//	luamerge/x        its external test imports lua/flow
//	moonscript/parse  embeds grammar.txt
//	moonscript        imports moonscript/parse
//	internal/tools/genparser  imports moonscript/parse
func fakeGraph(t *testing.T) *Graph {
	t.Helper()
	type mod struct {
		Path string
		Main bool
	}
	m := &mod{Path: "m", Main: true}
	pkgs := []map[string]any{
		{"ImportPath": "strings", "Standard": true},
		{"ImportPath": "m/lua/parse", "Dir": "/r/lua/parse", "Module": m, "Imports": []string{"strings"},
			"GoFiles": []string{"parse.go"}, "TestGoFiles": []string{"parse_test.go"}},
		{"ImportPath": "m/lua/parse [m/lua/parse.test]", "Dir": "/r/lua/parse", "Module": m, "ForTest": "m/lua/parse"},
		{"ImportPath": "m/lua/parse.test", "Dir": "/r/lua/parse", "Module": m,
			"Imports": []string{"m/lua/parse [m/lua/parse.test]"}},
		{"ImportPath": "m/lua", "Dir": "/r/lua", "Module": m, "GoFiles": []string{"vm.go"}},
		{"ImportPath": "m/lua/flow", "Dir": "/r/lua/flow", "Module": m, "Imports": []string{"m/lua/parse"},
			"GoFiles": []string{"flow.go"}, "TestGoFiles": []string{"flow_test.go"}},
		{"ImportPath": "m/lua/flow [m/lua/flow.test]", "Dir": "/r/lua/flow", "Module": m, "ForTest": "m/lua/flow",
			"Imports": []string{"m/lua/parse"}},
		{"ImportPath": "m/lua/flow.test", "Dir": "/r/lua/flow", "Module": m,
			"Imports": []string{"m/lua/flow [m/lua/flow.test]"}},
		{"ImportPath": "m/luamerge/x", "Dir": "/r/luamerge/x", "Module": m,
			"GoFiles": []string{"x.go"}, "XTestGoFiles": []string{"x_test.go"}},
		{"ImportPath": "m/luamerge/x_test [m/luamerge/x.test]", "Dir": "/r/luamerge/x", "Module": m,
			"ForTest": "m/luamerge/x", "Imports": []string{"m/luamerge/x", "m/lua/flow"}},
		{"ImportPath": "m/luamerge/x.test", "Dir": "/r/luamerge/x", "Module": m,
			"Imports": []string{"m/luamerge/x_test [m/luamerge/x.test]"}},
		{"ImportPath": "m/moonscript/parse", "Dir": "/r/moonscript/parse", "Module": m,
			"GoFiles": []string{"peg.go"}, "EmbedFiles": []string{"grammar.txt"}},
		{"ImportPath": "m/moonscript", "Dir": "/r/moonscript", "Module": m,
			"GoFiles": []string{"moonscript.go"}, "Imports": []string{"m/moonscript/parse"}},
		{"ImportPath": "m/internal/tools/genparser", "Dir": "/r/internal/tools/genparser", "Module": m,
			"GoFiles": []string{"main.go"}, "Imports": []string{"m/moonscript/parse"}},
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for _, p := range pkgs {
		if err := enc.Encode(p); err != nil {
			t.Fatal(err)
		}
	}
	g, err := parseGraph("/r", strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestSelect(t *testing.T) {
	g := fakeGraph(t)
	for _, tc := range []struct {
		name  string
		files []string
		want  string // the outputs that differ from a selection of nothing
	}{{
		name:  "code, through the tests",
		files: []string{"lua/parse/parse.go"},
		want: `packages=./lua/flow ./lua/parse ./luamerge/x go=true lua=true luamerge=true corpus=true goreleaser=true ` +
			`lint-trees=["root","luamerge"] race-trees=["luamerge"] changelog=missing`,
	}, {
		name:  "a test file selects its package only",
		files: []string{"lua/parse/parse_test.go"},
		want:  `packages=./lua/parse go=true lua=true corpus=true lint-trees=["root"]`,
	}, {
		name:  "a deleted Go file is code",
		files: []string{"lua/parse/gone.go"},
		want: `packages=./lua/flow ./lua/parse ./luamerge/x go=true lua=true luamerge=true corpus=true goreleaser=true ` +
			`lint-trees=["root","luamerge"] race-trees=["luamerge"] changelog=missing`,
	}, {
		name:  "testdata, read by the package and by DataDeps",
		files: []string{"lua/testdata/compat/x.lua"},
		want:  `packages=./lua ./lua/flow ./lua/parse go=true lua=true corpus=true lint-trees=["root"]`,
	}, {
		name:  "an embedded file is code",
		files: []string{"moonscript/parse/grammar.txt"},
		want: `packages=./internal/tools/genparser ./moonscript ./moonscript/parse go=true moonscript=true generate=true ` +
			`lint-trees=["root"]`,
	}, {
		name:  "documentation",
		files: []string{"luamerge/x/README.md", "README.md", ".github/PULL_REQUEST_TEMPLATE.md"},
		want:  `docs-only=true codespell=true`,
	}, {
		name:  "the changelog of the fork",
		files: []string{"lua/CHANGES.md", "lua/parse/parse.go"},
		want: `packages=./lua/flow ./lua/parse ./luamerge/x go=true lua=true luamerge=true corpus=true goreleaser=true ` +
			`lint-trees=["root","luamerge"] race-trees=["luamerge"]`,
	}, {
		name:  "goreleaser's config",
		files: []string{"luamerge/.goreleaser.yml"},
		want:  `goreleaser=true codespell=true`,
	}, {
		name:  "a file of no package",
		files: []string{"scripts/gen-parse-golden.sh"},
		want:  ``,
	}, {
		name:  "go.mod: everything",
		files: []string{"go.mod"},
		want: `packages=./... all=true go=true lua=true moonscript=true luamerge=true corpus=true generate=true goreleaser=true codespell=true ` +
			`lint-trees=["root","luamerge"] race-trees=["luamerge"]`,
	}, {
		name:  "the workflow: everything",
		files: []string{".github/workflows/ci.yml"},
		want: `packages=./... all=true go=true lua=true moonscript=true luamerge=true corpus=true generate=true goreleaser=true codespell=true ` +
			`lint-trees=["root","luamerge"] race-trees=["luamerge"]`,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := diffOutputs(Select(g, tc.files, false)); got != tc.want {
				t.Errorf("Select(%q):\n got %s\nwant %s", tc.files, got, tc.want)
			}
		})
	}
}

func TestSelectAll(t *testing.T) {
	r := Select(fakeGraph(t), []string{"lua/parse/parse.go"}, true)
	if !r.All || r.DocsOnly || r.Changelog != "missing" || len(r.Packages) != 7 {
		t.Errorf("Select(all) = %+v", r)
	}
}

// diffOutputs are the outputs of r that differ from those of a selection
// of nothing.
func diffOutputs(r *Result) string {
	none := (&Result{Changelog: "ok"}).Outputs()
	var out []string
	for i, o := range r.Outputs() {
		if o != none[i] {
			out = append(out, o[0]+"="+o[1])
		}
	}
	return strings.Join(out, " ")
}

func TestOwner(t *testing.T) {
	g := fakeGraph(t)
	for f, want := range map[string]string{
		"lua/parse/parse.go":         "lua/parse",
		"lua/parse/testdata/a/b.lua": "lua/parse",
		"lua/testdata/x.lua":         "lua",
		"lua/_tools/x.go":            "lua",
		"README.md":                  "",
		"scripts/x.sh":               "",
	} {
		got := ""
		if p := g.Owner(f); p != nil {
			got = p.Dir
		}
		if got != want {
			t.Errorf("Owner(%s) = %q, want %q", f, got, want)
		}
	}
}

func TestTestDeps(t *testing.T) {
	got := slices.Sorted(maps.Keys(fakeGraph(t).TestDeps("luamerge/x")))
	want := []string{"lua/flow", "lua/parse", "luamerge/x"}
	if !slices.Equal(got, want) {
		t.Errorf("TestDeps(luamerge/x) = %q, want %q", got, want)
	}
}

func TestRules(t *testing.T) {
	for f, want := range map[string]bool{
		"go.mod":                            true,
		"luamerge/go.mod":                   false,
		".github/workflows/ci.yml":          true,
		".github/PULL_REQUEST_TEMPLATE.md":  false,
		".github/ISSUE_TEMPLATE/bug.yml":    false,
		"internal/tools/affected/rules.go":  true,
		"internal/tools/affectedx/rules.go": false,
		"scripts/ci/run-checks.sh":          true,
		"scripts/check-gopher-lua-suite.sh": true,
		"scripts/gen-parse-golden.sh":       false,
		"luamerge/.golangci.yml":            true,
		"moonscript/testdata/corpus/go.mod": false,
	} {
		if got := WholeRepo(f); got != want {
			t.Errorf("WholeRepo(%s) = %v, want %v", f, got, want)
		}
	}
	for _, d := range DataDeps {
		if filepath.IsAbs(d.Reader) || d.Reader == "" {
			t.Errorf("DataDeps: reader %q", d.Reader)
		}
		for _, p := range d.Paths {
			if strings.HasPrefix(p, d.Reader+"/") {
				t.Errorf("DataDeps: %s reads %s, in its own directory", d.Reader, p)
			}
		}
	}
	if got := changelog([]string{"lua/vm.go", "lua/vm_test.go"}); got != "missing" {
		t.Errorf("changelog(vm.go) = %s", got)
	}
	if got := changelog([]string{"lua/vm_test.go", "lua/testdata/compat/x.lua", "luamerge/x.go"}); got != "ok" {
		t.Errorf("changelog(tests) = %s", got)
	}
}
