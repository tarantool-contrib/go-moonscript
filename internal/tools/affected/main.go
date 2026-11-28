// Command affected selects what CI runs for a change: the packages whose
// tests are affected, and the jobs of the areas it touches.
//
//	go run ./internal/tools/affected -base origin/master -head HEAD
//	go run ./internal/tools/affected lua/parse/parser.go   # the files given
//	go run ./internal/tools/affected -all                  # everything
//	go run ./internal/tools/affected verify-data           # check DataDeps (strace)
//
// The changed files are those of git diff base...head (from the merge
// base). A file selects the package whose directory is its nearest
// ancestor: its code (with every package whose code or tests import it,
// through go list -deps -test ./... of the head tree), or its tests only
// (a _test.go file, testdata). DataDeps adds the tests that read data
// outside their package, and a file of WholeRepo selects everything.
//
// -github appends the selection to $GITHUB_OUTPUT (the job's outputs) and
// a summary to $GITHUB_STEP_SUMMARY: packages (./dir ..., or ./... for
// everything), all, go, lua, moonscript, luascope, luamerge, docs-only,
// corpus, generate, goreleaser, codespell, lint-trees and race-trees (JSON
// arrays, for a matrix), changelog (ok or missing).
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "verify-data" {
		if err := verifyMain(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "affected verify-data:", err)
			os.Exit(1)
		}
		return
	}
	if err := selectMain(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "affected:", err)
		os.Exit(1)
	}
}

func selectMain(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("affected", flag.ContinueOnError)
	base := fs.String("base", "", "the base `ref`: the changed files are those of git diff base...head")
	head := fs.String("head", "HEAD", "the head `ref`")
	all := fs.Bool("all", false, "select everything (the changed files still decide changelog)")
	root := fs.String("C", "", "the repository root (default: the top level of the current git work tree)")
	github := fs.Bool("github", false, "write the outputs to $GITHUB_OUTPUT and a summary to $GITHUB_STEP_SUMMARY")
	asJSON := fs.Bool("json", false, "print the selection as JSON")
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
	files := fs.Args()
	if *base != "" {
		if len(files) != 0 {
			return errors.New("files and -base are exclusive")
		}
		var err error
		if files, err = ChangedFiles(*root, *base, *head); err != nil {
			return err
		}
	} else if len(files) == 0 && !*all {
		return errors.New("no changed files: give -base, files or -all")
	}
	g, err := LoadGraph(*root)
	if err != nil {
		return err
	}
	r := Select(g, files, *all)
	if *github {
		if err := writeGitHub(r); err != nil {
			return err
		}
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	_, err = io.WriteString(stdout, r.Summary(false))
	return err
}

// ChangedFiles lists the files git diff base...head changes, a rename
// as both names.
func ChangedFiles(root, base, head string) ([]string, error) {
	out, err := git(root, "diff", "--name-only", "--no-renames", "-z", base+"..."+head)
	if err != nil {
		return nil, err
	}
	var files []string
	for f := range strings.SplitSeq(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stderr.Bytes())
	}
	return string(out), nil
}

// Outputs are the job outputs, in order.
func (r *Result) Outputs() [][2]string {
	pkgs := "./..."
	if !r.All {
		var b strings.Builder
		for i, p := range r.Packages {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("./" + p)
		}
		pkgs = b.String()
	}
	list := func(l []string) string {
		if l == nil {
			l = []string{}
		}
		b, _ := json.Marshal(l)
		return string(b)
	}
	flag := strconv.FormatBool
	return [][2]string{
		{"packages", pkgs},
		{"all", flag(r.All)},
		{"go", flag(r.Go)},
		{"lua", flag(r.Lua)},
		{"moonscript", flag(r.MoonScript)},
		{"luascope", flag(r.Luascope)},
		{"luamerge", flag(r.Luamerge)},
		{"docs-only", flag(r.DocsOnly)},
		{"corpus", flag(r.Corpus)},
		{"generate", flag(r.Generate)},
		{"goreleaser", flag(r.Goreleaser)},
		{"codespell", flag(r.Codespell)},
		{"lint-trees", list(r.LintTrees)},
		{"race-trees", list(r.RaceTrees)},
		{"changelog", r.Changelog},
	}
}

// Jobs are the jobs of a pull request's run of ci.yml the selection runs
// (with their conditions in ci.yml: keep them in sync), besides changes,
// changelog and ci-ok, which always run. A step is "job/step".
func (r *Result) Jobs() []string {
	var jobs []string
	add := func(on bool, names ...string) {
		if on {
			jobs = append(jobs, names...)
		}
	}
	add(r.Go, "vet")
	for _, t := range r.LintTrees {
		jobs = append(jobs, "lint("+t+")")
	}
	add(r.Go, "test(1.26.x)", "test(1.27.x)")
	add(r.Go && r.Luamerge, "test/lua-interpreters")
	add(r.Go && r.Lua, "test/gopher-lua")
	add(r.All, "coverage")
	for _, t := range r.RaceTrees {
		jobs = append(jobs, "testrace("+t+")")
	}
	add(r.Go, "govulncheck")
	add(r.Codespell, "codespell")
	add(r.Generate, "generate")
	add(r.Goreleaser, "goreleaser")
	add(r.Corpus, "corpus")
	if len(jobs) == 0 {
		return []string{"(none)"}
	}
	return jobs
}

// Summary describes the selection, in Markdown if md.
func (r *Result) Summary(md bool) string {
	var b strings.Builder
	code := func(s string) string {
		if md {
			return "`" + s + "`"
		}
		return s
	}
	if md {
		b.WriteString("### Change detection\n\n")
	}
	fmt.Fprintf(&b, "changed files: %d\n", len(r.Files))
	if len(r.Why) != 0 {
		why := strings.Join(r.Why[:min(len(r.Why), 5)], " ")
		if len(r.Why) > 5 {
			why += fmt.Sprintf(" (and %d more)", len(r.Why)-5)
		}
		fmt.Fprintf(&b, "everything, for: %s\n", code(why))
	} else if r.All {
		b.WriteString("everything (full checks)\n")
	}
	fmt.Fprintf(&b, "jobs: %s\n", code(strings.Join(r.Jobs(), " ")))
	if md {
		b.WriteString("\n| output | value |\n| --- | --- |\n")
	}
	for _, o := range r.Outputs() {
		v := o[1]
		if o[0] == "packages" && !r.All {
			v = fmt.Sprintf("%d: %s", len(r.Packages), v)
		}
		if md {
			fmt.Fprintf(&b, "| %s | %s |\n", o[0], code(v))
		} else {
			fmt.Fprintf(&b, "%-11s %s\n", o[0]+":", v)
		}
	}
	return b.String()
}

func writeGitHub(r *Result) error {
	appendTo := func(env, s string) error {
		name := os.Getenv(env)
		if name == "" {
			return fmt.Errorf("$%s is not set", env)
		}
		f, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, s)
		return errors.Join(err, f.Close())
	}
	var b strings.Builder
	for _, o := range r.Outputs() {
		fmt.Fprintf(&b, "%s=%s\n", o[0], o[1])
	}
	if err := appendTo("GITHUB_OUTPUT", b.String()); err != nil {
		return err
	}
	return appendTo("GITHUB_STEP_SUMMARY", r.Summary(true))
}
