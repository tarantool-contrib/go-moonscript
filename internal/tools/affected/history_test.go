package main

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The tests on this repository's history: the changed files of real
// commits, selected in the package graph of the current tree. They skip
// in a clone without the commits (a shallow one).

const repoRoot = "../../.."

var (
	graphOnce sync.Once
	repoGraph *Graph
	graphErr  error
)

func headGraph(t *testing.T) *Graph {
	t.Helper()
	graphOnce.Do(func() { repoGraph, graphErr = LoadGraph(repoRoot) })
	if graphErr != nil {
		t.Fatal(graphErr)
	}
	return repoGraph
}

func historyFiles(t *testing.T, base, head string) []string {
	t.Helper()
	for _, rev := range []string{base, head} {
		if _, err := git(repoRoot, "rev-parse", "--verify", "--quiet", rev+"^{commit}"); err != nil {
			t.Skipf("commit %s is not in this clone", rev)
		}
	}
	files, err := ChangedFiles(repoRoot, base, head)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestHistory(t *testing.T) {
	for _, tc := range []struct {
		name       string
		base, head string
		outputs    map[string]string // outputs to compare, by name
		has, hasNo []string          // packages selected, and not
		tree       string            // if set, every package is of this tree
	}{{
		name: "lua/parse: Lua52Compat",
		base: "0a68259^", head: "0a68259",
		outputs: map[string]string{
			"all": "false", "lua": "true", "moonscript": "true", "luascope": "true", "luamerge": "true",
			"corpus": "true", "goreleaser": "true", "codespell": "false", "docs-only": "false",
			"lint-trees": `["root","luascope","luamerge"]`, "race-trees": `["luascope","luamerge"]`,
			"changelog": "missing",
		},
		has: []string{
			"lua/parse", "lua/format", "lua/scope", "lua/flow", "lua", "internal/luasuites",
			"luamerge/internal/parse", "luamerge/internal/lex", "luamerge/cmd/luamerge",
			"luascope", "luascope/globals", "moonscript/runtime", "cmd/moon",
		},
		hasNo: []string{"lua/pm", "moonscript/parse", "moonscript/compiler", "luamerge/internal/config"},
	}, {
		name: "luamerge only: the lexer",
		base: "9906388^", head: "9906388",
		outputs: map[string]string{
			"all": "false", "lua": "false", "moonscript": "false", "luascope": "false", "luamerge": "true",
			"corpus": "false", "generate": "false", "goreleaser": "true", "codespell": "true",
			"lint-trees": `["luamerge"]`, "race-trees": `["luamerge"]`, "changelog": "ok",
		},
		has:  []string{"luamerge/internal/lex", "luamerge/internal/parse", "luamerge", "luamerge/cmd/luamerge"},
		tree: "luamerge",
	}, {
		name: "luamerge only: four commits",
		base: "4b91a53^", head: "34dc21e",
		outputs: map[string]string{
			"all": "false", "lua": "false", "luascope": "false", "luamerge": "true", "lint-trees": `["luamerge"]`,
		},
		tree: "luamerge",
	}, {
		name: "documentation only",
		base: "34dc21e^", head: "34dc21e",
		outputs: map[string]string{
			"packages": "", "all": "false", "go": "false", "luamerge": "false", "docs-only": "true",
			"goreleaser": "false", "codespell": "true", "lint-trees": "[]", "race-trees": "[]",
		},
	}, {
		name: "luascope: docs and doc.go",
		base: "3ef775e^", head: "3ef775e",
		outputs: map[string]string{
			"all": "false", "lua": "false", "luascope": "true", "luamerge": "false", "docs-only": "false",
			"lint-trees": `["luascope"]`, "race-trees": `["luascope"]`,
		},
		has:  []string{"luascope"},
		tree: "luascope",
	}, {
		name: "moonscript testdata: the corpus module",
		base: "9467e5c^", head: "9467e5c",
		outputs: map[string]string{
			"all": "false", "lua": "false", "moonscript": "true", "corpus": "true", "luamerge": "false",
		},
		has:   []string{"moonscript", "internal/tools/corpus", "cmd/moon", "lua/format"},
		hasNo: []string{"moonscript/parse", "lua/parse", "moonscript/compiler"},
	}, {
		name: "go.mod: everything",
		base: "0d4cc7b^", head: "0d4cc7b",
		outputs: map[string]string{
			"packages": "./...", "all": "true", "lua": "true", "moonscript": "true", "luascope": "true",
			"luamerge": "true", "corpus": "true", "generate": "true", "goreleaser": "true", "codespell": "true",
		},
	}, {
		name: "the workflow: everything",
		base: "d284706^", head: "d284706",
		outputs: map[string]string{
			"packages": "./...", "all": "true", "changelog": "ok",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			files := historyFiles(t, tc.base, tc.head)
			r := Select(headGraph(t), files, false)
			for _, o := range r.Outputs() {
				if want, ok := tc.outputs[o[0]]; ok && o[1] != want {
					t.Errorf("%s = %s, want %s", o[0], o[1], want)
				}
			}
			for _, p := range tc.has {
				if !slices.Contains(r.Packages, p) {
					t.Errorf("%s is not selected", p)
				}
			}
			for _, p := range tc.hasNo {
				if slices.Contains(r.Packages, p) {
					t.Errorf("%s is selected", p)
				}
			}
			if tc.tree != "" {
				for _, p := range r.Packages {
					if Tree(p) != tc.tree && p != SelfDir {
						t.Errorf("%s is selected, not of %s", p, tc.tree)
					}
				}
			}
			if t.Failed() {
				t.Logf("files: %q\n%s", files, r.Summary(false))
			}
		})
	}
}

// TestHistoryInvariants selects every commit since the monorepo's first
// (the fork's move to lua/) and checks what holds for any change.
func TestHistoryInvariants(t *testing.T) {
	const first = "b93335e"
	historyFiles(t, first, "HEAD")
	out, err := git(repoRoot, "rev-list", "--no-merges", first+"^..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	g := headGraph(t)
	for c := range strings.FieldsSeq(out) {
		files, err := ChangedFiles(repoRoot, c+"^", c)
		if err != nil {
			t.Fatal(err)
		}
		r := Select(g, files, false)
		if r.DocsOnly && (r.Go || r.All) {
			t.Errorf("%.7s: docs-only with packages", c)
		}
		if r.All && len(r.Packages) != len(g.Packages) {
			t.Errorf("%.7s: all, but %d packages of %d", c, len(r.Packages), len(g.Packages))
		}
		for _, f := range files {
			p := g.Packages[path.Dir(f)]
			if strings.HasSuffix(f, ".go") && p != nil && !slices.Contains(r.Packages, p.Dir) {
				t.Errorf("%.7s: %s changed, %s is not selected", c, f, p.Dir)
			}
			if IsDoc(f) && !r.All && r.Lua && len(files) == 1 {
				t.Errorf("%.7s: documentation %s selects lua", c, f)
			}
		}
	}
}

// TestGenerateInputs: every //go:generate directive's package and the
// generator it runs are GenerateInputs.
func TestGenerateInputs(t *testing.T) {
	out, err := git(repoRoot, "grep", "-n", "^//go:generate", "--", "*.go")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for line := range strings.Lines(out) {
		file, _, _ := strings.Cut(line, ":")
		dir := path.Dir(file)
		if !slices.Contains(GenerateInputs, dir) {
			t.Errorf("%s has a //go:generate directive: add %s to GenerateInputs", file, dir)
		}
		if _, run, ok := strings.Cut(line, "go run "); ok {
			target, _, _ := strings.Cut(strings.TrimSpace(run), " ")
			target = path.Join(dir, target)
			if !slices.Contains(GenerateInputs, target) {
				t.Errorf("%s runs %s: add it to GenerateInputs", file, target)
			}
		}
		n++
	}
	if n == 0 {
		t.Error("no //go:generate directive")
	}
}

// TestFuzzMatrix: the nightly fuzz job of ci.yml runs every fuzz target
// of the repository.
func TestFuzzMatrix(t *testing.T) {
	out, err := git(repoRoot, "grep", "-h", "-o", "-E", `^func Fuzz[A-Za-z0-9_]*`, "--", "*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for line := range strings.Lines(out) {
		targets = append(targets, strings.TrimSpace(strings.TrimPrefix(line, "func ")))
	}
	ci, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix []string
	for _, m := range regexp.MustCompile(`\{pkg: \S+, target: (\w+)\}`).FindAllSubmatch(ci, -1) {
		matrix = append(matrix, string(m[1]))
	}
	slices.Sort(targets)
	slices.Sort(matrix)
	if !slices.Equal(targets, matrix) {
		t.Errorf("the fuzz job of ci.yml runs %q, the fuzz targets are %q", matrix, targets)
	}
}

// TestDataDepsReaders: the readers of DataDeps are packages with tests.
func TestDataDepsReaders(t *testing.T) {
	g := headGraph(t)
	for _, d := range DataDeps {
		p := g.Packages[d.Reader]
		if p == nil || len(p.Test) == 0 {
			t.Errorf("DataDeps: %s is not a package with tests", d.Reader)
		}
	}
}
