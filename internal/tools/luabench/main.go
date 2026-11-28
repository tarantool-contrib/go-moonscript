// Command luabench compares the speed of our VM (lua/
// with the runtime's Lua 5.1 compatibility layer, set up as cmd/moon sets
// it up) with PUC Lua 5.1.5 and LuaJIT 2.1 (interpreter, luajit -joff, and
// JIT) on LuaJIT's benchmark suite (LuaJIT-test-cleanup bench/ at a pinned
// commit) and on MoonScript benchmarks of our own (testdata/bench/moon,
// compiled by our compiler; the same Lua runs on every engine).
//
// Usage (from the repository root; scripts/luabench.sh builds and runs it):
//
//	go build -o luabench ./internal/tools/luabench
//	./luabench [-n 5] [-bench fasta,moon/*] [-engines vm,puc,luajit-joff,luajit]
//	           [-p name=param,...] [-json out.json] [-compare old.json]
//	           [-pprof dir] [-verify=false] [-list]
//	./luabench vm [-cpuprofile f] wrap.lua script.lua [args...]   # our VM's runner
//
// Engines: "vm" is a host process (`luabench vm`, a fresh state per run);
// PUC Lua is lua5.1 of image moonref:0.8.0 ($MOONREF), LuaJIT is luajit of
// image luajitref:2.1 ($LUAJITREF, moonref plus Debian's luajit
// 2.1.0-beta3). One long-lived container per image; runs are docker exec's.
//
// Measurement: every engine runs the script under the same wrapper
// (wrap.lua), which reports the CPU seconds of the script's run alone, so
// Docker's and the interpreters' startup are outside it (the "empty"
// benchmark is that baseline, ~0). The clock is os.clock (clock(3), the
// process CPU time) on PUC Lua and LuaJIT and getrusage(RUSAGE_SELF) on our
// VM (user + system time of the process, GC worker threads included).
// Each benchmark runs -n times per engine, rounds interleaving the
// benchmarks and rotating the engine order; the table shows the median.
//
// Correctness: each run's output (minus timings a program prints, see
// bench.mask) must equal PUC Lua's, or it is reported as a failure, not
// a time; with -verify, the programs listed in the suite's TEST_md5sum.txt
// are also run with its parameters and their checksums compared.
//
// Files: the suite is fetched (git, SSH remote; $LUABENCH_REPO overrides)
// into the cache directory, $LUABENCH_CACHE or
// ~/.cache/go-moonscript/luajit-bench, which also holds the work directory
// (scripts, generated inputs, outputs) and results/<time>.json, the
// machine-readable results (-json), which -compare reads back. Programs
// whose license allows it are vendored in testdata/bench/lua (SOURCES.md).
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed wrap.lua
var wrapLua []byte

func main() {
	if len(os.Args) > 1 && os.Args[1] == "vm" {
		os.Exit(vmMain(os.Args[2:]))
	}
	os.Exit(benchMain())
}

var (
	flagN       = flag.Int("n", 5, "runs per benchmark and engine")
	flagBench   = flag.String("bench", "", "comma-separated benchmark names or globs (default: all)")
	flagEngines = flag.String("engines", "vm,puc,luajit-joff,luajit", "comma-separated engines")
	flagParams  = flag.String("p", "", "parameter overrides, name=param,...")
	flagCache   = flag.String("cache", defaultCache(), "cache directory ($LUABENCH_CACHE)")
	flagRoot    = flag.String("root", "", "go-moonscript checkout (default: found from the working directory)")
	flagJSON    = flag.String("json", "", "results file (default <cache>/results/<time>.json)")
	flagCompare = flag.String("compare", "", "compare with the results of an earlier run (its JSON file)")
	flagVerify  = flag.Bool("verify", true, "also check the TEST_md5sum.txt checksums")
	flagPprof   = flag.String("pprof", "", "after the timed runs, write a CPU profile of one more run on our VM per benchmark to `dir`")
	flagTimeout = flag.Duration("timeout", 5*time.Minute, "timeout of one run")
	flagList    = flag.Bool("list", false, "list the benchmarks and the skipped programs")
	flagPUC     = flag.String("puc-image", envOr("MOONREF", "moonref:0.8.0"), "image with /usr/bin/lua5.1")
	flagJIT     = flag.String("luajit-image", envOr("LUAJITREF", "luajitref:2.1"), "image with /usr/bin/luajit")
)

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func defaultCache() string {
	if d := os.Getenv("LUABENCH_CACHE"); d != "" {
		return d
	}
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "go-moonscript", "luajit-bench")
}

func benchMain() int {
	flag.Parse()
	if *flagList {
		for i := range suite {
			b := &suite[i]
			fmt.Printf("%-20s %-8s %s\n", b.name, b.param, b.input)
		}
		for _, s := range skipped {
			fmt.Printf("%-20s skipped: %s\n", s.name, s.reason)
		}
		return 0
	}
	if err := runBench(); err != nil {
		fmt.Fprintln(os.Stderr, "luabench:", err)
		return 1
	}
	return 0
}

func selectBenches() ([]*bench, error) {
	overrides := map[string]string{}
	for kv := range strings.SplitSeq(*flagParams, ",") {
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("-p: %q is not name=param", kv)
		}
		overrides[k] = v
	}
	var pats []string
	for p := range strings.SplitSeq(*flagBench, ",") {
		if p != "" {
			pats = append(pats, p)
		}
	}
	var out []*bench
	for i := range suite {
		b := suite[i]
		if len(pats) > 0 && !slices.ContainsFunc(pats, func(p string) bool {
			ok, _ := path.Match(p, b.name)
			return ok
		}) {
			continue
		}
		if v, ok := overrides[b.name]; ok {
			b.param = v
			if b.input != "" {
				kind, _, _ := strings.Cut(b.input, ":")
				b.input = kind + ":" + v
			}
			delete(overrides, b.name)
		}
		out = append(out, &b)
	}
	for k := range overrides {
		return nil, fmt.Errorf("-p: no benchmark %q", k)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no benchmark matches -bench %q", *flagBench)
	}
	return out, nil
}

func findRoot() (string, error) {
	if *flagRoot != "" {
		return filepath.Abs(*flagRoot)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil &&
			strings.Contains(string(data), "module github.com/tarantool-contrib/go-moonscript\n") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not in a go-moonscript checkout (use -root)")
		}
		dir = parent
	}
}

func runBench() error {
	benches, err := selectBenches()
	if err != nil {
		return err
	}
	root, err := findRoot()
	if err != nil {
		return err
	}
	cache, err := filepath.Abs(*flagCache)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	ws := &workspace{dir: filepath.Join(cache, "work"), cache: cache, root: root}
	if err := ws.prepare(benches); err != nil {
		return err
	}
	engines, err := newEngines(strings.Split(*flagEngines, ","), *flagPUC, *flagJIT)
	if err != nil {
		return err
	}
	stop, err := startContainers(engines, ws.dir)
	if err != nil {
		return err
	}
	defer stop()

	runOne := func(e *engine, b *bench, script, param, input, out, profile string) runResult {
		var args []string
		if param != "" {
			args = strings.Fields(param)
		}
		stdin := ""
		if input != "" {
			gen := engines[0]
			for _, e := range engines {
				if e.name == "puc" {
					gen = e
				}
			}
			var ierr error
			stdin, ierr = ws.inputFile(input, func(script, param, _, stdout string) error {
				return gen.run(context.Background(), self, ws.dir, script, []string{param}, "", stdout, "", nil).err
			})
			if ierr != nil {
				return runResult{err: ierr}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), *flagTimeout)
		defer cancel()
		return e.run(ctx, self, ws.dir, filepath.Join(ws.dir, script), args, stdin, out, profile, b.mask)
	}

	rep := &report{
		Version: 1, Date: time.Now().Format(time.RFC3339), SuiteCommit: suiteCommit,
		Go: runtime.Version(), GOMAXPROCS: runtime.GOMAXPROCS(0), NumCPU: runtime.NumCPU(),
		Reps: *flagN, Images: map[string]string{"puc": *flagPUC, "luajit": *flagJIT},
		Revision: revision(root),
	}
	for _, e := range engines {
		rep.Engines = append(rep.Engines, e.name)
	}
	for _, s := range skipped {
		rep.Skipped = append(rep.Skipped, skipJSON{s.name, s.reason})
	}
	results := make([]*benchResult, len(benches))
	for i, b := range benches {
		results[i] = &benchResult{Name: b.name, Param: b.param, Input: b.input, Engines: map[string]*engineResult{}}
		for _, e := range engines {
			results[i].Engines[e.name] = &engineResult{Status: "ok"}
		}
	}
	rep.Benchmarks = results

	if *flagVerify {
		for i, b := range benches {
			if b.md5 == "" {
				continue
			}
			for _, e := range engines {
				out := filepath.Join(ws.dir, "out", fmt.Sprintf("%s.%s.md5run", strings.ReplaceAll(b.name, "/", "_"), e.name))
				r := runOne(e, b, b.script(), b.md5Param, b.md5Input, out, "")
				v := "ok"
				switch {
				case r.err != nil:
					v = "error: " + r.err.Error()
				case r.md5 != b.md5:
					v = "MISMATCH " + r.md5
				}
				results[i].Engines[e.name].Verify = v
				fmt.Fprintf(os.Stderr, "verify %-16s %-12s %s\n", b.name, e.name, v)
			}
		}
	}

	rep.LoadStart = loadavg()
	fmt.Fprintf(os.Stderr, "luabench: %d benchmarks x %d engines x %d runs, load %.2f\n",
		len(benches), len(engines), *flagN, rep.LoadStart)
	for round := 0; round < *flagN; round++ {
		rep.RoundLoad = append(rep.RoundLoad, loadavg())
		for i, b := range benches {
			for k := range engines {
				e := engines[(k+round+i)%len(engines)]
				er := results[i].Engines[e.name]
				if strings.HasPrefix(er.Status, "error") {
					continue
				}
				out := filepath.Join(ws.dir, "out", fmt.Sprintf("%s.%s.out", strings.ReplaceAll(b.name, "/", "_"), e.name))
				r := runOne(e, b, b.script(), b.param, b.input, out, "")
				if r.err != nil {
					er.Status = "error: " + r.err.Error()
					fmt.Fprintf(os.Stderr, "[%d/%d] %-20s %-12s %s\n", round+1, *flagN, b.name, e.name, er.Status)
					continue
				}
				er.Times = append(er.Times, r.cpu)
				er.Wall = append(er.Wall, r.wall)
				if er.MD5 == "" {
					er.MD5 = r.md5
				} else if er.MD5 != r.md5 {
					er.Status = "nondeterministic output"
				}
				fmt.Fprintf(os.Stderr, "[%d/%d] %-20s %-12s %8.3fs\n", round+1, *flagN, b.name, e.name, r.cpu)
			}
		}
	}
	rep.LoadEnd = loadavg()
	for _, br := range results {
		ref := br.Engines["puc"]
		for name, er := range br.Engines {
			er.Median, er.Min = median(er.Times), minimum(er.Times)
			if name != "puc" && ref != nil && ref.Status == "ok" && er.Status == "ok" && er.MD5 != ref.MD5 {
				er.Status = "wrong output (differs from PUC 5.1)"
			}
		}
	}

	if *flagPprof != "" {
		if err := os.MkdirAll(*flagPprof, 0o755); err != nil {
			return err
		}
		for _, e := range engines {
			if e.name != "vm" {
				continue
			}
			for _, b := range benches {
				prof := filepath.Join(*flagPprof, strings.ReplaceAll(b.name, "/", "_")+".pprof")
				out := filepath.Join(ws.dir, "out", strings.ReplaceAll(b.name, "/", "_")+".pprof.out")
				if r := runOne(e, b, b.script(), b.param, b.input, out, prof); r.err != nil {
					fmt.Fprintf(os.Stderr, "pprof %s: %v\n", b.name, r.err)
				}
			}
		}
	}

	printTable(os.Stdout, rep)
	jsonPath := *flagJSON
	if jsonPath == "" {
		jsonPath = filepath.Join(cache, "results", time.Now().Format("20060102-150405")+".json")
	}
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "luabench: results in", jsonPath)
	if *flagCompare != "" {
		old, err := readReport(*flagCompare)
		if err != nil {
			return err
		}
		printCompare(os.Stdout, old, rep, *flagCompare)
	}
	return nil
}

// loadavg is the 1-minute load average.
func loadavg() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return -1
	}
	f, _ := strconv.ParseFloat(strings.Fields(string(data))[0], 64)
	return f
}

func revision(root string) string {
	cmd := exec.Command("git", "describe", "--always", "--dirty")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
