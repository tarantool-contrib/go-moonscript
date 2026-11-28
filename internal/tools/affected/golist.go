package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// listedPackage is the part of `go list -json` the tool reads.
type listedPackage struct {
	ImportPath string
	Dir        string
	ForTest    string
	Standard   bool
	Module     *struct {
		Path string
		Main bool
	}
	Imports []string

	GoFiles, CgoFiles, CFiles, CXXFiles, HFiles, SFiles []string
	SysoFiles, IgnoredGoFiles, IgnoredOtherFiles        []string
	EmbedFiles                                          []string

	TestGoFiles, XTestGoFiles       []string
	TestEmbedFiles, XTestEmbedFiles []string
}

const listFields = "ImportPath,Dir,ForTest,Standard,Module,Imports," +
	"GoFiles,CgoFiles,CFiles,CXXFiles,HFiles,SFiles,SysoFiles,IgnoredGoFiles,IgnoredOtherFiles,EmbedFiles," +
	"TestGoFiles,XTestGoFiles,TestEmbedFiles,XTestEmbedFiles"

// Package is a package of the main module.
type Package struct {
	Path string // import path
	Dir  string // relative to the repository root, slash-separated; "." is the root

	// files relative to the repository root
	Code []string // what the package's own build reads: Go, cgo, assembly, embedded files
	Test []string // what only its tests' build reads: _test.go files and their embedded files
}

// Graph is the package graph of the main module, with the tests of every
// package: what `go list -deps -test ./...` prints.
type Graph struct {
	Module   string              // the main module's path
	Packages map[string]*Package // by directory
	byPath   map[string]*Package

	// nodes are the packages of the build graph with the test variants
	// ("p [q.test]", "p_test [p.test]", "p.test"), by import path as go
	// list prints it; deps are their edges, rdeps the reverse edges.
	rdeps map[string][]string
	deps  map[string][]string
	nodes map[string]*node
}

type node struct {
	path   string // the import path without the " [q.test]" suffix
	tested string // the import path of the package whose test this is part of
}

// LoadGraph runs go list in the repository root dir.
func LoadGraph(root string) (*Graph, error) {
	cmd := exec.Command("go", "list", "-e", "-deps", "-test", "-json="+listFields, "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w\n%s", err, stderr.Bytes())
	}
	return parseGraph(root, bytes.NewReader(out))
}

func parseGraph(root string, r io.Reader) (*Graph, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	g := &Graph{
		Packages: map[string]*Package{},
		byPath:   map[string]*Package{},
		rdeps:    map[string][]string{},
		deps:     map[string][]string{},
		nodes:    map[string]*node{},
	}
	var listed []*listedPackage
	dec := json.NewDecoder(r)
	for {
		var p listedPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list output: %w", err)
		}
		if p.Standard || p.Module == nil || !p.Module.Main {
			continue
		}
		if g.Module == "" {
			g.Module = p.Module.Path
		}
		listed = append(listed, &p)
	}
	for _, p := range listed {
		if p.ForTest != "" || strings.HasSuffix(p.ImportPath, ".test") || strings.Contains(p.ImportPath, " [") {
			continue
		}
		dir, err := filepath.Rel(root, p.Dir)
		if err != nil || dir == ".." || strings.HasPrefix(dir, "../") {
			return nil, fmt.Errorf("package %s outside the repository: %s", p.ImportPath, p.Dir)
		}
		dir = filepath.ToSlash(dir)
		pkg := &Package{Path: p.ImportPath, Dir: dir}
		join := func(files ...[]string) []string {
			var out []string
			for _, f := range slices.Concat(files...) {
				out = append(out, path.Join(dir, filepath.ToSlash(f)))
			}
			slices.Sort(out)
			return out
		}
		pkg.Code = join(p.GoFiles, p.CgoFiles, p.CFiles, p.CXXFiles, p.HFiles, p.SFiles,
			p.SysoFiles, p.IgnoredGoFiles, p.IgnoredOtherFiles, p.EmbedFiles)
		pkg.Test = join(p.TestGoFiles, p.XTestGoFiles, p.TestEmbedFiles, p.XTestEmbedFiles)
		g.Packages[dir] = pkg
		g.byPath[p.ImportPath] = pkg
	}
	for _, p := range listed {
		n := &node{path: p.ImportPath, tested: p.ForTest}
		if i := strings.Index(n.path, " ["); i >= 0 {
			n.path = n.path[:i]
		}
		if n.tested == "" {
			n.tested = strings.TrimSuffix(n.path, ".test")
		}
		g.nodes[p.ImportPath] = n
		g.deps[p.ImportPath] = p.Imports
		for _, imp := range p.Imports {
			g.rdeps[imp] = append(g.rdeps[imp], p.ImportPath)
		}
	}
	// BuildDeps: the reader (and its test variants) depends on the
	// packages it builds
	for _, b := range BuildDeps {
		reader := g.Packages[b.Reader]
		if reader == nil {
			continue
		}
		for id, n := range g.nodes {
			if n.path != reader.Path {
				continue
			}
			for _, dir := range b.Builds {
				if q := g.Packages[dir]; q != nil {
					g.deps[id] = append(g.deps[id], q.Path)
					g.rdeps[q.Path] = append(g.rdeps[q.Path], id)
				}
			}
		}
	}
	return g, nil
}

// Owner is the package whose directory is the nearest ancestor of file
// (relative to the root, slash-separated), nil if there is none.
func (g *Graph) Owner(file string) *Package {
	for dir := path.Dir(file); ; dir = path.Dir(dir) {
		if p := g.Packages[dir]; p != nil {
			return p
		}
		if dir == "." || dir == "/" {
			return nil
		}
	}
}

// Dependents is the set of packages of the main module (by directory)
// whose tests build one of the packages pkgs (by import path) or a test
// variant of one: the reverse dependency closure, through the tests.
func (g *Graph) Dependents(pkgs []string) map[string]bool {
	seen := map[string]bool{}
	var queue []string
	for id, n := range g.nodes {
		if slices.Contains(pkgs, n.path) && !strings.HasSuffix(id, ".test") && !strings.HasSuffix(n.path, "_test") {
			seen[id] = true
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, r := range g.rdeps[id] {
			if !seen[r] {
				seen[r] = true
				queue = append(queue, r)
			}
		}
	}
	out := map[string]bool{}
	for id := range seen {
		if p := g.byPath[g.nodes[id].tested]; p != nil {
			out[p.Dir] = true
		}
	}
	return out
}

// TestDeps is the set of packages of the main module (by directory) that
// the test of the package in dir builds, the package itself included.
func (g *Graph) TestDeps(dir string) map[string]bool {
	out := map[string]bool{}
	p := g.Packages[dir]
	if p == nil {
		return out
	}
	seen := map[string]bool{}
	var walk func(string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		if n := g.nodes[id]; n != nil {
			if q := g.byPath[n.path]; q != nil {
				out[q.Dir] = true
			}
		}
		for _, imp := range g.deps[id] {
			walk(imp)
		}
	}
	for id, n := range g.nodes {
		if n.tested == p.Path {
			walk(id)
		}
	}
	return out
}
