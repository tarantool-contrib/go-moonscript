package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// verify-data checks DataDeps against what the tests do: it builds the
// test binary of every package, runs it in the package's directory under
// strace -f, and classifies each path of the repository a test names
// outside its package's directory:
//
//   - whole: a WholeRepo file (go.mod), which selects everything anyway;
//   - table: a path of the package's DataDeps;
//   - imports: a file the build of a package the test imports reads (its
//     source, read as data, or by a go build the test runs), which a
//     change selects the test for already;
//   - missing: none of these. A missing read fails the check: add it to
//     DataDeps.
//
// Documentation a test reads in its own directory is missing too: a
// change of documentation selects no test (IsDoc). A path of DataDeps no
// test named is reported, as stale, without failing. The reads of git
// (the go command's VCS stamping), and those of this tool's own tests
// (which load the whole package graph: any change of a package selects
// them, see Select), are not checked.

// goProbes are the files the go command looks for in every directory
// up to the root.
var goProbes = map[string]bool{
	"go.mod": true, "go.work": true, "vendor": true,
	".git": true, ".hg": true, ".bzr": true, ".svn": true, ".fossil": true, "_FOSSIL_": true, ".fslckout": true,
	"default.pgo": true,
}

// Read kinds.
const (
	readOwn     = "own"
	readWhole   = "whole"
	readTable   = "table"
	readImports = "imports"
	readMissing = "missing"
)

// classify says why the test of pkg may name rel (relative to the root),
// and the DataDeps path that covers it, if any.
func classify(g *Graph, pkg *Package, testDeps map[string]bool, rel string, byGo bool) (kind, covered string) {
	if rel == "." || rel == ".git" || strings.HasPrefix(rel, ".git/") ||
		pkg.Dir == "." || pkg.Dir == SelfDir || strings.HasPrefix(pkg.Dir+"/", rel+"/") {
		return readOwn, "" // the root, .git, an ancestor of the package's directory
	}
	owner := g.Packages[rel]
	if owner == nil {
		owner = g.Owner(rel)
	}
	if owner == pkg {
		if IsDoc(rel) && !slices.Contains(pkg.Code, rel) && !slices.Contains(pkg.Test, rel) {
			if c := coveredBy(pkg.Dir, rel); c != "" {
				return readTable, c
			}
			return readMissing, ""
		}
		return readOwn, ""
	}
	if WholeRepo(rel) {
		return readWhole, ""
	}
	if c := coveredBy(pkg.Dir, rel); c != "" {
		return readTable, c
	}
	// a package the test builds: its directory, its files; the go command
	// a test runs (go build) also reads the other .go files of the
	// directory and looks at all its files
	if owner != nil && testDeps[owner.Dir] &&
		(rel == owner.Dir || slices.Contains(owner.Code, rel) ||
			byGo && (isGoSource(rel, owner.Dir) || path.Dir(rel) == owner.Dir)) {
		return readImports, ""
	}
	return readMissing, ""
}

// coveredBy is the path of the DataDeps of reader that covers rel.
func coveredBy(reader, rel string) string {
	for _, d := range DataDeps {
		if d.Reader != reader {
			continue
		}
		for _, p := range d.Paths {
			if (DataDep{Paths: []string{p}}).covers(rel) {
				return p
			}
		}
	}
	return ""
}

type pkgReport struct {
	pkg     *Package
	err     error
	elapsed time.Duration
	reads   map[string]map[string]bool // kind: paths
	used    map[string]bool            // DataDeps paths covering a read
}

func verifyMain(args []string) error {
	fs := flag.NewFlagSet("affected verify-data", flag.ContinueOnError)
	root := fs.String("C", "", "the repository root (default: the top level of the current git work tree)")
	jobs := fs.Int("j", 2, "test binaries run in parallel")
	filter := fs.String("pkg", "", "check only the packages whose directory matches the `regexp`")
	timeout := fs.Duration("timeout", 30*time.Minute, "the timeout of a test binary")
	straceBin := fs.String("strace", "strace", "the strace `command`")
	verbose := fs.Bool("v", false, "print every path outside the packages, not only the missing ones")
	keep := fs.Bool("keep", false, "keep the work directory (traces and logs)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *root == "" {
		out, err := git(".", "rev-parse", "--show-toplevel")
		if err != nil {
			return err
		}
		*root = strings.TrimSpace(out)
	}
	re, err := regexp.Compile(*filter)
	if err != nil {
		return err
	}
	g, err := LoadGraph(*root)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "affected-verify-")
	if err != nil {
		return err
	}
	if *keep {
		fmt.Println("work directory:", work)
	} else {
		defer func() { _ = os.RemoveAll(work) }()
	}

	var pkgs []*Package
	for _, dir := range slices.Sorted(maps.Keys(g.Packages)) {
		p := g.Packages[dir]
		if re.MatchString(dir) && slices.ContainsFunc(p.Test, func(f string) bool { return strings.HasSuffix(f, "_test.go") }) {
			pkgs = append(pkgs, p)
		}
	}
	reports := make([]*pkgReport, len(pkgs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(*jobs, 1))
	var mu sync.Mutex
	for i, p := range pkgs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			r := verifyPackage(g, p, *root, work, *straceBin, *timeout)
			reports[i] = r
			mu.Lock()
			status := "ok"
			if r.err != nil {
				status = "FAIL: " + r.err.Error()
			}
			fmt.Printf("%-34s %6.1fs  %s\n", p.Dir, r.elapsed.Seconds(), status)
			mu.Unlock()
		})
	}
	wg.Wait()

	fmt.Println()
	failed := false
	used := map[string]bool{}
	for _, r := range reports {
		if r.err != nil {
			failed = true
		}
		for k := range r.used {
			used[k] = true
		}
		kinds := []string{readMissing}
		if *verbose {
			kinds = []string{readTable, readImports, readWhole, readMissing}
		}
		for _, k := range kinds {
			if len(r.reads[k]) == 0 {
				continue
			}
			if k == readMissing {
				failed = true
			}
			fmt.Printf("%s: %s (%d paths)\n", r.pkg.Dir, k, len(r.reads[k]))
			for _, line := range summarize(slices.Sorted(maps.Keys(r.reads[k]))) {
				fmt.Println("    " + line)
			}
		}
	}
	if *filter == "" {
		for _, d := range DataDeps {
			for _, p := range d.Paths {
				if !used[d.Reader+"\x00"+p] {
					fmt.Printf("stale: DataDeps %s %s: no test of the package named it\n", d.Reader, p)
				}
			}
		}
	}
	if failed {
		return errors.New("failed: test binaries failed, or tests read files DataDeps does not list")
	}
	fmt.Println("DataDeps covers every file the tests name outside their packages")
	return nil
}

func verifyPackage(g *Graph, p *Package, root, work, straceBin string, timeout time.Duration) *pkgReport {
	start := time.Now()
	r := &pkgReport{pkg: p, reads: map[string]map[string]bool{}, used: map[string]bool{}}
	defer func() { r.elapsed = time.Since(start) }()
	name := strings.ReplaceAll(p.Dir, "/", "_")
	bin := filepath.Join(work, name+".test")
	build := exec.Command("go", "test", "-c", "-o", bin, "./"+p.Dir)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		r.err = fmt.Errorf("go test -c: %w\n%s", err, out)
		return r
	}
	trace := filepath.Join(work, name+".strace")
	logFile := filepath.Join(work, name+".log")
	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, straceBin, "-f", "--seccomp-bpf", "-qq", "-y",
		"-e", "trace="+strings.Join(tracedSyscalls, ","), "-o", trace,
		"--", bin, "-test.count=1", "-test.timeout="+timeout.String())
	cmd.Dir = filepath.Join(root, filepath.FromSlash(p.Dir))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	_ = os.WriteFile(logFile, out.Bytes(), 0o644)
	if err != nil {
		tail := out.Bytes()
		if len(tail) > 4000 {
			tail = tail[len(tail)-4000:]
		}
		r.err = fmt.Errorf("%s: %w\n%s", bin, err, tail)
		return r
	}
	f, err := os.Open(trace)
	if err != nil {
		r.err = err
		return r
	}
	defer func() { _ = f.Close() }()
	accesses, err := ParseStrace(f, cmd.Dir)
	if err != nil {
		r.err = err
		return r
	}
	r.add(g, p, root, accesses)
	return r
}

func (r *pkgReport) add(g *Graph, p *Package, root string, accesses []Access) {
	deps := g.TestDeps(p.Dir)
	rootSlash := filepath.ToSlash(root)
	for _, a := range accesses {
		rel, ok := strings.CutPrefix(a.Path, rootSlash)
		if !ok || rel != "" && rel[0] != '/' {
			continue // outside the repository
		}
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			rel = "."
		}
		if path.Base(a.Exe) == "git" {
			continue // git status of the go command's VCS stamping
		}
		if !a.OK && goProbes[path.Base(rel)] {
			continue // the go command looking for the module's root, the VCS, vendor/
		}
		kind, covered := classify(g, p, deps, rel, path.Base(a.Exe) == "go")
		if kind == readOwn {
			continue
		}
		if covered != "" {
			r.used[p.Dir+"\x00"+covered] = true
		}
		if r.reads[kind] == nil {
			r.reads[kind] = map[string]bool{}
		}
		if !a.OK {
			rel += " (absent)"
		}
		r.reads[kind][rel] = true
	}
}

// summarize shortens a sorted list of paths: the paths of a directory with
// more than 3 of them become "dir/ (n paths)".
func summarize(paths []string) []string {
	var out []string
	for i := 0; i < len(paths); {
		dir := path.Dir(strings.TrimSuffix(paths[i], " (absent)"))
		j := i
		for j < len(paths) && path.Dir(strings.TrimSuffix(paths[j], " (absent)")) == dir {
			j++
		}
		if j-i > 3 {
			out = append(out, fmt.Sprintf("%s/ (%d paths: %s ...)", dir, j-i, path.Base(paths[i])))
		} else {
			out = append(out, paths[i:j]...)
		}
		i = j
	}
	return out
}
