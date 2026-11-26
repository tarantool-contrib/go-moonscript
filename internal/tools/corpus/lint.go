package main

// Lint mode (-lint): compares the Go linter (package lint, moonc -l) with
// the reference moonscript.cmd.lint.
//
//	$C ref -lint        # reference lint results for the selected files
//	                      (one Docker container running -ref-j lua5.1
//	                      processes); only missing ones unless -force
//	$C -lint [-j N]     # compare, print the summary, write the report
//	$C -lint -diff ID   # full reference/Go lint output for one file
//
// Files: the distinct files of the moonscript segment (-segment selects
// others, -filter narrows). Each file is linted as lint_code(src,
// "<id>.moon") with every stage and the default whitelist; both output
// formats are compared (compact carries the columns, default the line
// rendering).
//
// Layout: ref-lint/<id>.status|.default|.compact|.err (see reflint.lua),
// reports/lint-report.txt, reports/lint-history.tsv.

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tarantool-contrib/go-moonscript/lint"
)

//go:embed reflint.lua
var reflintLua []byte

// Lint outcomes.
const (
	LMatch         = "match"          // same status and output
	LMismatch      = "mismatch"       // both linted, reports differ
	LStatus        = "status-differs" // e.g. ref clean, Go reports; ref lint, Go crashes
	LCrashMatch    = "crash-match"    // both died with the same (table) compile error
	LCrashMismatch = "crash-mismatch" // both died, different errors
	LGoPanic       = "go-panic"       // Go runtime panic (a port bug), ref did not crash
	LNoRef         = "no-ref"         // no usable reference result
)

// lintFiles are the distinct files lint mode works on.
func lintFiles(dir string) ([]*Occurrence, error) {
	all, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	segs := map[string]bool{"moonscript": true}
	if *flagSegment != "" {
		segs = map[string]bool{}
		for _, s := range strings.Split(*flagSegment, ",") {
			segs[strings.TrimSpace(s)] = true
		}
	}
	seen := map[string]bool{}
	var files []*Occurrence
	for _, f := range all {
		if !segs[f.Segment] || seen[f.ID] || !f.matches(*flagFilter) {
			continue
		}
		seen[f.ID] = true
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files selected")
	}
	return files, nil
}

// genLintRef runs reflint.lua over the selected files in a single
// container, with -ref-j lua processes inside it.
func genLintRef(dir string) error {
	files, err := lintFiles(dir)
	if err != nil {
		return err
	}
	refDir := filepath.Join(dir, "ref-lint")
	scriptDir := filepath.Join(dir, "scripts")
	for _, d := range []string{refDir, scriptDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "reflint.lua"), reflintLua, 0o644); err != nil {
		return err
	}

	for round := 1; ; round++ {
		var todo []string
		for _, f := range files {
			st := readStatus(refDir, f.ID)
			if st == "" || st == "pending" || (*flagForce && round == 1) {
				todo = append(todo, f.ID)
			}
		}
		if len(todo) == 0 {
			break
		}
		n := max(min(*flagRefJobs, len(todo)), 1)
		shards := make([]strings.Builder, n)
		for i, id := range todo {
			shards[i%n].WriteString(id + ".moon\n")
		}
		var script strings.Builder
		for i := range shards {
			name := fmt.Sprintf(".shard-%d", i)
			if err := os.WriteFile(filepath.Join(refDir, name), []byte(shards[i].String()), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(&script, "lua5.1 /scripts/reflint.lua /files /ref < /ref/%s > /ref/%s.out & ", name, name)
		}
		script.WriteString("wait")

		fmt.Printf("lint ref round %d: %d files, %d processes in one container\n", round, len(todo), n)
		start := time.Now()
		name := fmt.Sprintf("moonref-lint-%d", os.Getpid())
		timeout := *flagRefTO
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--name", name,
			"--network", "none", "--memory", "4g",
			"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
			"-v", filepath.Join(dir, "files")+":/files:ro",
			"-v", refDir+":/ref",
			"-v", scriptDir+":/scripts:ro",
			*flagImage, "sh", "-c", script.String())
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		err := cmd.Run()
		timedOut := ctx.Err() != nil
		cancel()
		if timedOut {
			_ = exec.Command("docker", "kill", name).Run() // best effort: it may be gone already
		}
		fmt.Printf("lint ref round %d done in %s\n", round, time.Since(start).Round(time.Second))
		if err == nil && !timedOut {
			break
		}
		status := "ref-crash"
		if timedOut {
			status = "ref-timeout"
		}
		marked := 0
		for _, id := range todo {
			if readStatus(refDir, id) == "pending" {
				fmt.Printf("lint ref: %s -> %s\n", id, status)
				if err := os.WriteFile(filepath.Join(refDir, id+".status"), []byte(status+"\n"), 0o644); err != nil {
					return err
				}
				marked++
			}
		}
		if marked == 0 {
			return fmt.Errorf("reference container failed without a pending file: %w", err)
		}
	}

	counts := map[string]int{}
	for _, f := range files {
		counts[readStatus(refDir, f.ID)]++
	}
	fmt.Println("reference lint results:")
	for _, k := range sortedKeys(counts) {
		name := k
		if name == "" {
			name = "(missing)"
		}
		fmt.Printf("  %-18s %d\n", name, counts[k])
	}
	return nil
}

// lintOut is one side's lint result, in reflint.lua's terms. Hashed: a
// reference known by the Digest of its lintKey only (see expected.go).
type lintOut struct {
	Status           string // clean | lint | parse-error | crash-table | crash-string | go-panic
	Default, Compact string
	Err              string
	Hashed           bool
	Digest           string
}

func loadLintRef(dir, id string) lintOut {
	if m := mustExpected(dir); m != nil && !hasRefOutputs(dir, "ref-lint", id) {
		o, err := parseLintEntry(m[id].lint)
		if err != nil {
			return lintOut{Status: "missing", Hashed: true}
		}
		return o
	}
	refDir := filepath.Join(dir, "ref-lint")
	read := func(ext string) string {
		b, _ := os.ReadFile(filepath.Join(refDir, id+"."+ext))
		return string(b)
	}
	return lintOut{Status: readStatus(refDir, id), Default: read("default"), Compact: read("compact"), Err: read("err")}
}

// goLint lints src like reflint.lua does.
func goLint(id, src string) (out lintOut) {
	name := id + ".moon"
	defer func() {
		if r := recover(); r != nil {
			out = lintOut{Status: "go-panic", Err: fmt.Sprint(r)}
		}
	}()
	for _, format := range []string{"default", "compact"} {
		res, ok, err := lint.Code(src, name, nil, lint.Options{Format: format})
		var crash *lint.Crash
		switch {
		case errors.As(err, &crash):
			if crash.Table {
				return lintOut{Status: "crash-table", Err: crashFields(crash)}
			}
			if strings.HasPrefix(crash.Msg, "runtime error:") || strings.Contains(crash.Msg, "interface conversion") {
				return lintOut{Status: "go-panic", Err: crash.Msg}
			}
			return lintOut{Status: "crash-string", Err: crash.Msg}
		case err != nil:
			return lintOut{Status: "parse-error", Err: err.Error()}
		case !ok:
			return lintOut{Status: "clean"}
		}
		out.Status = "lint"
		if format == "default" {
			out.Default = res
		} else {
			out.Compact = res
		}
	}
	return out
}

// crashFields renders a table crash like reflint.lua: kind, msg, pos.
func crashFields(c *lint.Crash) string {
	pos := "nil"
	if c.Pos != 0 {
		pos = fmt.Sprint(c.Pos)
	}
	return "user-error\n" + c.Msg + "\n" + pos
}

type lintResult struct {
	ID, Outcome, Ref, Go string
	Detail               string // first difference
}

func classifyLint(ref, got lintOut) (outcome, detail string) {
	switch {
	case ref.Status == "" || ref.Status == "pending" || strings.HasPrefix(ref.Status, "ref-"):
		return LNoRef, ref.Status
	case got.Status == "go-panic":
		return LGoPanic, firstLines(got.Err, 3)
	case ref.Hashed:
		return classifyLintDigest(ref, got)
	case ref.Status != got.Status:
		var d string
		if got.Status == "crash-table" || got.Status == "crash-string" {
			d = "go: " + firstLines(got.Err, 3)
		} else if ref.Status == "crash-table" || ref.Status == "crash-string" {
			d = "ref: " + firstLines(ref.Err, 3)
		} else {
			d = firstDiff(ref.Compact+ref.Err, got.Compact+got.Err)
		}
		return LStatus, d
	case ref.Status == "crash-table":
		// the kind (user-error / compile-error) isn't kept by the port
		r := strings.SplitN(ref.Err, "\n", 2)
		g := strings.SplitN(got.Err, "\n", 2)
		if len(r) == 2 && len(g) == 2 && r[1] == g[1] {
			return LCrashMatch, ""
		}
		return LCrashMismatch, firstDiff(ref.Err, got.Err)
	case ref.Status == "crash-string":
		// reference messages carry a file:line: prefix
		if strings.HasSuffix(ref.Err, got.Err) {
			return LCrashMatch, ""
		}
		return LCrashMismatch, firstDiff(ref.Err, got.Err)
	case ref.Status == "parse-error":
		if ref.Err == got.Err {
			return LMatch, ""
		}
		return LMismatch, firstDiff(ref.Err, got.Err)
	}
	if ref.Compact != got.Compact {
		return LMismatch, firstDiff(ref.Compact, got.Compact)
	}
	if ref.Default != got.Default {
		return LMismatch, "default format: " + firstDiff(ref.Default, got.Default)
	}
	return LMatch, ""
}

// classifyLintDigest is classifyLint for a Hashed reference.
func classifyLintDigest(ref, got lintOut) (outcome, detail string) {
	crash := ref.Status == "crash-table" || ref.Status == "crash-string"
	switch {
	case ref.Status != got.Status:
		d := "ref " + ref.Status + ", go " + got.Status
		if got.Err != "" {
			d += ": " + firstLines(got.Err, 3)
		}
		return LStatus, d
	case ref.Status == "clean":
		return LMatch, ""
	case digest(lintKey(got, false)) == ref.Digest:
		if crash {
			return LCrashMatch, ""
		}
		return LMatch, ""
	case crash:
		return LCrashMismatch, "go: " + firstLines(got.Err, 3) + " (reference: digest " + ref.Digest + ")"
	}
	return LMismatch, "differs from the expected digest " + ref.Digest
}

// firstDiff describes the first differing line.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b || i >= len(w) || i >= len(g) {
			return fmt.Sprintf("line %d: ref %q go %q", i+1, truncate(a, 160), truncate(b, 160))
		}
	}
	return ""
}

func runLint(dir string) error {
	files, err := lintFiles(dir)
	if err != nil {
		return err
	}
	if _, err := loadExpected(dir); err != nil {
		return err
	}
	start := time.Now()
	jobs := make(chan *Occurrence)
	results := make(chan lintResult)
	var wg sync.WaitGroup
	for range max(*flagJobs, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				ref := loadLintRef(dir, f.ID)
				src, err := os.ReadFile(filepath.Join(dir, "files", f.ID+".moon"))
				if err != nil {
					results <- lintResult{ID: f.ID, Outcome: LNoRef, Ref: ref.Status, Detail: err.Error()}
					continue
				}
				got := goLint(f.ID, string(src))
				outcome, detail := classifyLint(ref, got)
				results <- lintResult{f.ID, outcome, ref.Status, got.Status, detail}
			}
		}()
	}
	go func() {
		for _, f := range files {
			jobs <- f
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	byID := map[string]*Occurrence{}
	for _, f := range files {
		byID[f.ID] = f
	}
	var res []lintResult
	for r := range results {
		res = append(res, r)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].ID < res[j].ID })
	elapsed := time.Since(start).Round(time.Millisecond)

	counts := map[string]int{}
	refCounts := map[string]int{}
	for _, r := range res {
		counts[r.Outcome]++
		refCounts[r.Ref]++
	}
	var sum strings.Builder
	fmt.Fprintf(&sum, "lint: %d files in %s\n", len(res), elapsed)
	fmt.Fprintf(&sum, "  reference statuses:")
	for _, k := range sortedKeys(refCounts) {
		fmt.Fprintf(&sum, " %s %d", k, refCounts[k])
	}
	sum.WriteString("\n")
	for _, k := range []string{LMatch, LCrashMatch, LMismatch, LStatus, LCrashMismatch, LGoPanic, LNoRef} {
		fmt.Fprintf(&sum, "  %-16s %d\n", k, counts[k])
	}
	fmt.Print(sum.String())

	var rep strings.Builder
	fmt.Fprintf(&rep, "corpus lint report %s (corpus %s, filter %q)\n\n%s\n", time.Now().Format(time.RFC3339), dir,
		*flagFilter, sum.String())
	for _, r := range res {
		if r.Outcome == LMatch || r.Outcome == LCrashMatch {
			continue
		}
		fmt.Fprintf(&rep, "%s %s (ref %s, go %s) %s\n    %s\n", r.Outcome, r.ID, r.Ref, r.Go, byID[r.ID].Name(), r.Detail)
	}
	reports := filepath.Join(dir, "reports")
	if err := os.MkdirAll(reports, 0o755); err != nil {
		return err
	}
	reportPath := *flagReport
	if reportPath == "" {
		reportPath = filepath.Join(reports, "lint-report.txt")
		if *flagFilter != "" {
			reportPath = filepath.Join(reports, "lint-report-partial.txt")
		}
	}
	if err := os.WriteFile(reportPath, []byte(rep.String()), 0o644); err != nil {
		return err
	}
	if *flagFilter == "" {
		hist := filepath.Join(reports, "lint-history.tsv")
		segs := *flagSegment
		if segs == "" {
			segs = "moonscript"
		}
		line := fmt.Sprintf("%s\t%s\t%d\tmatch %d\tcrash-match %d\tmismatch %d\tstatus %d\tcrash-mismatch %d\tgo-panic %d\tno-ref %d\n",
			time.Now().Format(time.RFC3339), segs, len(res), counts[LMatch], counts[LCrashMatch], counts[LMismatch],
			counts[LStatus], counts[LCrashMismatch], counts[LGoPanic], counts[LNoRef])
		f, err := os.OpenFile(hist, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(line)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	fmt.Printf("\nreport: %s\nfull diff for one file: corpus-bin -lint -diff <id>\n", reportPath)
	if bad := len(res) - counts[LMatch] - counts[LCrashMatch]; *flagStrict && bad > 0 {
		return fmt.Errorf("-strict: %d files do not match the reference", bad)
	}
	return nil
}

// showLintDiff prints both sides' lint output for one file.
func showLintDiff(dir, sel string) error {
	files, err := lintFiles(dir)
	if err != nil {
		return err
	}
	var hit *Occurrence
	for _, f := range files {
		if f.ID == sel {
			hit = f
		}
	}
	if hit == nil {
		return fmt.Errorf("-lint -diff %q: no such file id", sel)
	}
	src, err := os.ReadFile(filepath.Join(dir, "files", hit.ID+".moon"))
	if err != nil {
		return err
	}
	ref, got := loadLintRef(dir, hit.ID), goLint(hit.ID, string(src))
	outcome, detail := classifyLint(ref, got)
	fmt.Printf("%s  %s\nsource: %s\noutcome: %s %s\n", hit.ID, hit.Name(), filepath.Join(dir, "files", hit.ID+".moon"), outcome, detail)
	show := func(label string, o lintOut) {
		fmt.Printf("\n=== %s: %s\n", label, o.Status)
		if o.Compact != "" {
			fmt.Printf("--- compact\n%s\n", o.Compact)
		}
		if o.Default != "" {
			fmt.Printf("--- default\n%s\n", o.Default)
		}
		if o.Err != "" {
			fmt.Printf("--- err\n%s\n", o.Err)
		}
	}
	if ref.Hashed {
		fmt.Printf("\n=== reference: %s, digest %s\n%s\n", ref.Status, orStr(ref.Digest, "-"), digestOnly)
	} else {
		show("reference", ref)
	}
	show("go", got)
	return nil
}
