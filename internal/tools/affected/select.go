package main

import (
	"maps"
	"path"
	"slices"
	"strings"
)

// Result is what a change selects.
type Result struct {
	Files []string // the changed files, relative to the root

	// All: a file in WholeRepo changed (Why lists them), or -all.
	All bool
	Why []string

	// Packages: the directories of the packages whose tests run,
	// relative to the root ("lua/parse"), sorted.
	Packages []string

	// The areas. Lua: a file under lua/ changed, other than
	// documentation. MoonScript, Luascope, Luamerge: a package of the
	// tree (Trees) is in Packages. DocsOnly: every changed file is
	// documentation (IsDoc), and none is read by a test.
	Lua, MoonScript, Luascope, Luamerge, DocsOnly bool

	Go         bool     // Packages is not empty: go vet, govulncheck, go mod tidy
	Corpus     bool     // the corpus job: Lua, or the corpus checker is affected
	Generate   bool     // go generate: a package of GenerateInputs is affected
	Goreleaser bool     // goreleaser check: Luamerge, or a file of GoreleaserInputs changed
	Codespell  bool     // a file under one of CodespellDirs changed
	LintTrees  []string // the golangci-lint configs to run: "root", "luascope", "luamerge"
	RaceTrees  []string // the trees of the -race -count=100 job: "luascope", "luamerge"

	// Changelog: "missing" if a non-test file under lua/ changed but
	// lua/CHANGES.md did not, else "ok".
	Changelog string
}

// Select computes what changing files selects in the graph g. all forces
// everything (the full checks); the changed files still decide Changelog.
func Select(g *Graph, files []string, all bool) *Result {
	r := &Result{Files: slices.Sorted(slices.Values(files)), Changelog: changelog(files)}
	r.Files = slices.Compact(r.Files)

	var code []string             // import paths of the packages whose code changed
	tests := map[string]bool{}    // directories of the packages whose tests' inputs changed
	docsOnly := len(r.Files) != 0 // until a file that is not documentation
	for _, f := range r.Files {
		if WholeRepo(f) {
			r.Why = append(r.Why, f)
		}
		readers := DataReaders(f)
		for _, d := range readers {
			tests[d] = true
		}
		doc := IsDoc(f)
		if !doc || len(readers) != 0 {
			docsOnly = false
		}
		if !doc && strings.HasPrefix(f, "lua/") {
			r.Lua = true
		}
		if dirOf(f, CodespellDirs) {
			r.Codespell = true
		}
		if slices.Contains(GoreleaserInputs, f) {
			r.Goreleaser = true
		}
		if slices.Contains(GenerateFiles, f) {
			r.Generate = true
		}
		p := g.Owner(f)
		if p == nil {
			continue
		}
		switch {
		case slices.Contains(p.Code, f) || isGoSource(f, p.Dir) && !strings.HasSuffix(f, "_test.go"):
			// a file the package's build reads, or one it no longer has
			code = append(code, p.Path)
		case slices.Contains(p.Test, f) || isGoSource(f, p.Dir):
			tests[p.Dir] = true
		case doc:
			// documentation, unless a data dependency lists it
		default:
			// testdata and other files of the package's directory
			tests[p.Dir] = true
		}
	}
	r.DocsOnly = docsOnly && len(r.Why) == 0 && !all
	r.All = all || len(r.Why) != 0

	pkgs := g.Dependents(code)
	maps.Copy(pkgs, tests)
	if r.All {
		for dir := range g.Packages {
			pkgs[dir] = true
		}
	}
	trees := map[string]bool{}
	for dir := range pkgs {
		if g.Packages[dir] != nil {
			r.Packages = append(r.Packages, dir)
			trees[Tree(dir)] = true
		}
	}
	if len(r.Packages) != 0 && !pkgs[SelfDir] && g.Packages[SelfDir] != nil {
		// this tool's tests check the selection on the package graph
		r.Packages = append(r.Packages, SelfDir)
	}
	slices.Sort(r.Packages)

	if r.All {
		r.Lua, r.Codespell, r.Goreleaser = true, true, true
	}
	r.Go = len(r.Packages) != 0
	for _, dir := range r.Packages {
		if slices.Contains(GenerateInputs, dir) {
			r.Generate = true
		}
		if dir == CorpusChecker {
			r.Corpus = true
		}
	}
	r.MoonScript = trees["moonscript"]
	r.Luascope = trees["luascope"]
	r.Luamerge = trees["luamerge"]
	r.Corpus = r.Corpus || r.Lua
	r.Goreleaser = r.Goreleaser || r.Luamerge
	for _, t := range []string{"root", "luascope", "luamerge"} {
		lint := t
		if t == "root" {
			// the root config lints every tree but luascope's and luamerge's
			lint = ""
			for tree := range trees {
				if tree != "luascope" && tree != "luamerge" {
					lint = t
				}
			}
		} else if !trees[t] {
			lint = ""
		}
		if lint != "" {
			r.LintTrees = append(r.LintTrees, lint)
		}
	}
	for _, t := range []string{"luascope", "luamerge"} {
		if trees[t] {
			r.RaceTrees = append(r.RaceTrees, t)
		}
	}
	return r
}

// isGoSource: f is a .go file directly in dir.
func isGoSource(f, dir string) bool {
	return strings.HasSuffix(f, ".go") && path.Dir(f) == dir
}

// dirOf: f is under one of the directories dirs ("dir/").
func dirOf(f string, dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(f, d) {
			return true
		}
	}
	return false
}

func changelog(files []string) string {
	code := false
	for _, f := range files {
		switch {
		case f == ChangesFile:
			return "ok"
		case strings.HasPrefix(f, "lua/") && !isTestFile(f):
			code = true
		}
	}
	if code {
		return "missing"
	}
	return "ok"
}

// isTestFile: a _test.go file or a file of a testdata directory.
func isTestFile(f string) bool {
	return strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "testdata/") || strings.Contains(f, "/testdata/")
}
