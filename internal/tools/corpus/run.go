package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

var segmentTitles = map[string]string{
	"moonscript":   " (real-world MoonScript)",
	segmentMutants: " (derived error cases of the moonscript files, repo ~mutant/...)",
	"moonlibs-lua": " (Tarantool Lua of github.com/moonlibs fed in as MoonScript)",
}

// workerMain serves check requests: one file id per stdin line, one JSON
// Result per stdout line.
func workerMain(dir string, parseOnly bool) error {
	debug.SetMaxStack(256 << 20) // fail fast on runaway recursion
	runtime.GOMAXPROCS(2)
	in := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(out)
	for in.Scan() {
		id := strings.TrimSpace(in.Text())
		src, err := os.ReadFile(filepath.Join(dir, "files", id+".moon"))
		var res Result
		if id == os.Getenv("MOON_CORPUS_CRASH_ID") {
			crashForTest() //nolint:staticcheck // SA4017: called only to crash the worker; exercises the supervisor's crash handling
		}
		if err != nil {
			res = Result{ID: id, Parse: PNoRef, Compile: CSkip}
		} else {
			res = check(id, string(src), loadRef(dir, id), parseOnly)
		}
		if err := enc.Encode(res); err != nil {
			return err
		}
		if err := out.Flush(); err != nil {
			return err
		}
	}
	return in.Err()
}

// capBuf keeps the first max bytes written since the last reset.
type capBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (c *capBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *capBuf) reset() {
	c.mu.Lock()
	c.buf.Reset()
	c.mu.Unlock()
}

func (c *capBuf) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

type workerProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan []byte
	stderr *capBuf
}

func startWorker(dir string, parseOnly bool) (*workerProc, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{"-worker", "-corpus", dir}
	if parseOnly {
		args = append(args, "-parse-only")
	}
	cmd := exec.Command(exe, args...)
	w := &workerProc{cmd: cmd, lines: make(chan []byte, 1), stderr: &capBuf{max: 64 << 10}}
	cmd.Stderr = w.stderr
	if w.stdin, err = cmd.StdinPipe(); err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		r := bufio.NewReaderSize(stdout, 1<<20)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				close(w.lines)
				return
			}
			w.lines <- line
		}
	}()
	return w, nil
}

func (w *workerProc) stop() {
	// the worker may already be dead: these fail then, and its exit status
	// does not matter (stderr has the details)
	_ = w.stdin.Close()
	_ = w.cmd.Process.Kill()
	_ = w.cmd.Wait()
}

// do checks one file; ok is false when the worker died or was killed (it
// must then be replaced).
func (w *workerProc) do(f *Occurrence, refStatus string, timeout time.Duration) (res Result, ok bool) {
	w.stderr.reset()
	start := time.Now()
	fatal := func(kind, detail string) Result {
		w.stop()
		r := Result{ID: f.ID, Ref: refStatus, Parse: PNoRef, Compile: CSkip, Fatal: kind,
			Ms: time.Since(start).Milliseconds()}
		if kind == FCrash {
			r.CompileBucket = crashBucket(detail)
			r.CompileExcerpt = firstLines(detail, 16)
		}
		return r
	}
	if _, err := io.WriteString(w.stdin, f.ID+"\n"); err != nil {
		return fatal(FCrash, "write to worker: "+err.Error()+"\n"+w.stderr.String()), false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case line, open := <-w.lines:
		if !open {
			_ = w.cmd.Wait() // reap the dead worker; stderr says why it died
			return fatal(FCrash, w.stderr.String()), false
		}
		if err := json.Unmarshal(line, &res); err != nil {
			return fatal(FCrash, "bad worker output: "+err.Error()), false
		}
		return res, true
	case <-timer.C:
		return fatal(FTimeout, ""), false
	}
}

// crashBucket summarizes a fatal worker death (stack overflow, fatal error,
// os.Exit).
func crashBucket(stderr string) string {
	head := "worker died"
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "fatal error:") || strings.HasPrefix(l, "panic:") ||
			strings.HasPrefix(l, "runtime:") {
			head = l
			if !strings.HasPrefix(l, "runtime:") {
				break
			}
		}
	}
	return normMsg(truncate(head, 120)) + " @ " + faultFrame(stderr)
}

func run(dir string) error {
	all, err := readManifest(dir)
	if err != nil {
		return err
	}
	var files []*Occurrence
	for _, f := range all {
		if f.matches(*flagFilter) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("no files match -filter %q", *flagFilter)
	}
	byID := map[string]*Occurrence{}
	for _, f := range files {
		byID[f.ID] = f
	}

	start := time.Now()
	jobs := make(chan *Occurrence)
	results := make(chan Result)
	var wg sync.WaitGroup
	refDir := filepath.Join(dir, "ref")
	for range max(*flagJobs, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var w *workerProc
			defer func() {
				if w != nil {
					w.stop()
				}
			}()
			for f := range jobs {
				st := readStatus(refDir, f.ID)
				if w == nil {
					var err error
					if w, err = startWorker(dir, *flagParseOnly); err != nil {
						results <- Result{ID: f.ID, Ref: st, Parse: PNoRef, Compile: CSkip, Fatal: FCrash,
							CompileBucket: "cannot start worker: " + err.Error()}
						continue
					}
				}
				res, ok := w.do(f, st, *flagTimeout)
				if !ok {
					w = nil
				}
				results <- res
			}
		}()
	}
	go func() {
		// largest files first: better load balancing
		order := append([]*Occurrence(nil), files...)
		sort.SliceStable(order, func(i, j int) bool { return order[i].Bytes > order[j].Bytes })
		for _, f := range order {
			jobs <- f
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var res []Result
	lastTick := time.Now()
	for r := range results {
		res = append(res, r)
		if time.Since(lastTick) > 3*time.Second {
			fmt.Fprintf(os.Stderr, "\r%d/%d files", len(res), len(files))
			lastTick = time.Now()
		}
	}
	fmt.Fprintf(os.Stderr, "\r%d/%d files in %s\n", len(res), len(files), time.Since(start).Round(time.Millisecond))
	sort.Slice(res, func(i, j int) bool { return res[i].ID < res[j].ID })

	// one summary per segment
	bySeg := map[string][]Result{}
	for _, r := range res {
		seg := byID[r.ID].Segment
		bySeg[seg] = append(bySeg[seg], r)
	}
	type group struct {
		label string
		s     *summary
	}
	var groups []group
	order := append([]string(nil), segmentOrder...)
	for _, seg := range sortedKeys(bySeg) {
		if !slices.Contains(order, seg) {
			order = append(order, seg)
		}
	}
	for _, seg := range order {
		if rs := bySeg[seg]; len(rs) > 0 {
			groups = append(groups, group{seg, summarize(rs)})
		}
	}
	summary := ""
	if n := len(all) - len(files); n > 0 {
		summary = fmt.Sprintf("(%d files filtered out)\n", n)
	}
	for _, g := range groups {
		summary += "\n== segment " + g.label + segmentTitles[g.label] + "\n" + g.s.text(0, *flagParseOnly)
	}
	fmt.Print(summary)

	reports := filepath.Join(dir, "reports")
	if err := os.MkdirAll(reports, 0o755); err != nil {
		return err
	}
	reportPath, resultsPath := *flagReport, filepath.Join(reports, "results.jsonl")
	if reportPath == "" {
		reportPath = filepath.Join(reports, "report.txt")
		if *flagFilter != "" || *flagParseOnly {
			reportPath = filepath.Join(reports, "report-partial.txt")
			resultsPath = filepath.Join(reports, "results-partial.jsonl")
		}
	}
	buckets := bucketize(res, byID)
	fmt.Print(topBuckets(buckets, *flagTop))

	var rep strings.Builder
	fmt.Fprintf(&rep, "corpus report %s (corpus %s, filter %q, %s)\n\n", time.Now().Format(time.RFC3339), dir, *flagFilter,
		time.Since(start).Round(time.Second))
	rep.WriteString(summary)
	rep.WriteString("\n")
	writeReport(&rep, buckets, *flagExamples)
	if err := os.WriteFile(reportPath, []byte(rep.String()), 0o644); err != nil {
		return err
	}
	var jl bytes.Buffer
	enc := json.NewEncoder(&jl)
	for _, r := range res {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	if err := os.WriteFile(resultsPath, jl.Bytes(), 0o644); err != nil {
		return err
	}
	for _, g := range groups {
		if err := appendHistory(filepath.Join(reports, "history.tsv"), g.label, g.s, time.Since(start)); err != nil {
			return err
		}
	}
	fmt.Printf("\nreport:  %s\nresults: %s\nhistory: %s\n", reportPath, resultsPath, filepath.Join(reports, "history.tsv"))
	fmt.Printf("full diff for one file: corpus-bin -diff <id or repo/path substring>\n")
	return nil
}

// crashForTest dies with a fatal (unrecoverable) stack overflow.
func crashForTest() int { return crashForTest() + 1 } //nolint:staticcheck // SA5007: the unbounded recursion is the point
