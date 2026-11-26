// Command corpus checks the Go port against the reference MoonScript 0.8.0
// compiler on a corpus of real-world MoonScript code.
//
// Corpus layout (default $MOON_CORPUS, else go-moonscript/corpus in the user
// cache directory, e.g. ~/.cache/go-moonscript/corpus; see -corpus):
//
//	repos/OWNER__NAME/    shallow sparse clones (fetch.sh): segment moonscript
//	repos-moonlibs/...    *.lua of github.com/moonlibs (fetch.sh --moonlibs):
//	                      segment moonlibs-lua, Lua fed in as MoonScript
//	files/<id>.moon       deduplicated sources, id = sha256[:16] (collect)
//	manifest.tsv          id, sha256, bytes, lines, segment, repo, commit,
//	                      path for every occurrence (collect); derived
//	                      error-case mutants of the moonscript files are
//	                      segment "mutants", repo "~mutant/<kind>" (mutate.go)
//	ref/<id>.status       reference outcome: ok | parse-error | compile-error
//	                      | compile-internal | ... (ref)
//	ref/<id>.tree|.lua|.err  reference tree dump, Lua output, error text
//	reports/report.txt    mismatch report of the last run, grouped in buckets
//	reports/results.jsonl per-file results of the last run
//	reports/history.tsv   one summary line per run
//
// Usage (build the tool once):
//
//	go build -o "$MOON_CORPUS/corpus-bin" ./internal/tools/corpus
//	C=$MOON_CORPUS/corpus-bin
//	internal/tools/corpus/fetch.sh [--rediscover] [--codesearch]  # clone repos
//	internal/tools/corpus/fetch.sh --moonlibs  # clone the moonlibs-lua segment
//	$C collect           # dedupe into files/ + mutants + manifest.tsv
//	$C ref               # reference outputs for new files (Docker)
//	$C [run] [flags]     # compare, print per-segment summary, write report
//	$C -diff ID|SUBSTR   # full tree/Lua/error diff for one file
//	$C ref -lint         # reference moonc -l results (ref-lint/, Docker)
//	$C -lint             # compare the Go linter (see lint.go)
//
// Every file is checked in a worker subprocess so that panics, fatal errors
// (stack overflow) and hangs (-timeout) only fail that file.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

var (
	flagCorpus     = flag.String("corpus", defaultCorpus(), "corpus directory")
	flagFilter     = flag.String("filter", "", "only files whose id or repo/path contains this substring")
	flagDiff       = flag.String("diff", "", "show the full tree/Lua/error diff for the file with this id or repo/path substring")
	flagJobs       = flag.Int("j", max(runtime.NumCPU()-1, 1), "parallel workers")
	flagTimeout    = flag.Duration("timeout", 30*time.Second, "per-file timeout")
	flagParseOnly  = flag.Bool("parse-only", false, "only compare parse trees / parse errors")
	flagExamples   = flag.Int("examples", 6, "example files listed per bucket in the report")
	flagReport     = flag.String("report", "", "report file (default <corpus>/reports/report.txt)")
	flagTop        = flag.Int("top", 8, "buckets per category printed in the summary")
	flagWorker     = flag.Bool("worker", false, "internal: run as a worker subprocess")
	flagForce      = flag.Bool("force", false, "ref: regenerate all reference outputs, not only missing ones")
	flagRefJobs    = flag.Int("ref-j", 4, "ref: parallel reference containers")
	flagRefTO      = flag.Duration("ref-timeout", 20*time.Minute, "ref: timeout for one reference container")
	flagImage      = flag.String("image", "moonref:0.8.0", "ref: reference Docker image")
	flagMaxBytes   = flag.Int64("max-bytes", 4<<20, "collect: skip files larger than this")
	flagMutants    = flag.Int("mutants", 3, "collect: derived error-case mutants per real file (0: none)")
	flagCPUProfile = flag.String("cpuprofile", "", "with -diff: write a CPU profile of checking that file")
	flagSegment    = flag.String("segment", "", "only these comma-separated segments (moonscript, mutants, moonlibs-lua)")
	flagNoMutants  = flag.Bool("no-mutants", false, "ignore the derived mutant files (repo ~mutant/...)")
	flagLint       = flag.Bool("lint", false, "lint mode: ref/run/-diff compare moonc -l instead of the compiler (see lint.go)")
)

func defaultCorpus() string {
	if d := os.Getenv("MOON_CORPUS"); d != "" {
		return d
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = "."
	}
	return filepath.Join(cache, "go-moonscript", "corpus")
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: corpus [collect|ref|run] [flags]\n\n")
		flag.PrintDefaults()
	}
	mode := "run"
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "collect" || args[0] == "ref" || args[0] == "run") {
		mode, args = args[0], args[1:]
	}
	if err := flag.CommandLine.Parse(args); err != nil {
		os.Exit(2)
	}
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}

	var err error
	switch {
	case *flagWorker:
		err = workerMain(*flagCorpus, *flagParseOnly)
	case mode == "collect":
		err = collect(*flagCorpus, *flagMaxBytes)
	case mode == "ref" && *flagLint:
		err = genLintRef(*flagCorpus)
	case mode == "ref":
		err = genRef(*flagCorpus)
	case *flagLint && *flagDiff != "":
		err = showLintDiff(*flagCorpus, *flagDiff)
	case *flagLint:
		err = runLint(*flagCorpus)
	case *flagDiff != "":
		err = showDiff(*flagCorpus, *flagDiff)
	default:
		err = run(*flagCorpus)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(1)
	}
}
