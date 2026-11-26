package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	moonscript "github.com/tarantool-contrib/go-moonscript"
)

// The benchmark suite: LuaJIT-test-cleanup's bench/ at a pinned commit.
const (
	suiteRepo   = "git@github.com:LuaJIT/LuaJIT-test-cleanup.git"
	suiteCommit = "014708bceb70550a3ab8d539cff14d9085ca9cb8"
)

// bench is one benchmark program.
type bench struct {
	name  string // "fasta", "moon/classes"
	file  string // the script, in the work directory
	param string // the argument for the timed runs ("" for none)
	// input is the standard input: "" (none), "fasta:N" (the output of
	// fasta.lua N) or "sumcol:N" (SUMCOL_1.txt N times)
	input string
	// md5Param, md5Input and md5 are the run of TEST_md5sum.txt and the
	// checksum of its output ("" if the file has none for the program)
	md5Param, md5Input, md5 string
	// mask, if set, removes the run-dependent part of the output (timings)
	// before outputs are compared
	mask *regexp.Regexp
	// needs lists extra files of the suite the script loads
	needs []string
}

// Parameters: chosen so that PUC Lua 5.1.5 takes roughly 0.5-2 s (the
// PARAM_x86.txt values are sized for LuaJIT's JIT, 10-100x more work).
var suite = []bench{
	{name: "empty", file: "empty.lua"},
	{name: "array3d", param: "120"},
	{name: "binary-trees", param: "12", md5Param: "10", md5: "7202f4e13df7abc5ad8c07f05fe9d644"},
	{name: "chameneos", param: "1e6", md5Param: "1e5", md5: "a629ce12f63050c6656bce175258cf8f"},
	{name: "coroutine-ring", param: "4e6"},
	{name: "fannkuch", param: "9", md5Param: "8", md5: "51e5e372cbc5471ea8940b20ad782319"},
	{name: "fasta", param: "7e5", md5Param: "1e5", md5: "78cd327de6f0a5667da0aa9349888279"},
	{name: "k-nucleotide", param: "1.5e5", input: "fasta:1.5e5",
		md5Input: "fasta:10000", md5: "88efb24c1fed533959ed84bb32c88142"},
	{name: "life"},
	{name: "mandelbrot", param: "850", md5Param: "200", md5: "cc65e64bd553ed18896de1dfe7fae3e5"},
	{name: "meteor", param: "3000", md5Param: "3000", md5: "9a65bb4b0a735ace1eaa4f2628f01026"},
	{name: "nbody", param: "2e5", md5Param: "1e4", md5: "e0361c898ba747117ec177f7b3b3359c"},
	{name: "nsieve", param: "8", md5Param: "4", md5: "767e02c93624995732e151932fa5f304"},
	{name: "nsieve-bit-fp", param: "7", md5Param: "4", md5: "767e02c93624995732e151932fa5f304"},
	{name: "partialsums", param: "3e6", md5Param: "1e5", md5: "33efb41c72f8ecfb5b36c99e32189a3f"},
	{name: "pidigits-nogmp", param: "1000", md5Param: "200", md5: "173a11a77bb1e72dd31254a760317428"},
	{name: "ray", param: "4 64"},
	{name: "recursive-ack", param: "9"},
	{name: "recursive-fib", param: "34"},
	{name: "revcomp", param: "2e6", input: "fasta:2e6",
		md5Input: "fasta:10000", md5: "47de276e2f72519b57b82da39f4c7592"},
	{name: "scimark-fft", param: "300", needs: []string{"scimark_lib.lua"}},
	{name: "scimark-lu", param: "50", needs: []string{"scimark_lib.lua"}},
	{name: "scimark-sor", param: "700", needs: []string{"scimark_lib.lua"}},
	{name: "scimark-sparse", param: "3000", needs: []string{"scimark_lib.lua"}},
	{name: "series", param: "4000",
		mask: regexp.MustCompile(`, [0-9.]+ s, [0-9.inf-]+ iterations/s`)},
	{name: "spectral-norm", param: "450", md5Param: "200", md5: "25f44bd552ccd9faa0ee2ae5617947e2"},
	{name: "sum-file", param: "5000", input: "sumcol:5000",
		md5Input: "sumcol:100", md5: "2ebd3caa45b31a2e74e436b645eab4b0"},

	// MoonScript benchmarks (testdata/bench/moon), compiled by our moonc
	{name: "moon/classes", param: "6000"},
	{name: "moon/comprehensions", param: "4000"},
	{name: "moon/strings", param: "80000"},
	{name: "moon/destructure", param: "60000"},
	{name: "moon/compile", param: "200"},
}

// skipped are the programs of the suite that are not run, with the reason.
var skipped = []struct{ name, reason string }{
	{"euler14-bit", "needs the LuaJIT bit library (require \"bit\"), which our VM and PUC Lua 5.1 lack"},
	{"mandelbrot-bit", "needs the LuaJIT bit library"},
	{"md5", "needs the LuaJIT bit library"},
	{"nsieve-bit", "needs the LuaJIT bit library"},
	{"scimark-2010-12-20", "the full SciMark: runs each kernel for a fixed time (os.clock calibration) and prints MFlops, " +
		"so its work and output depend on the engine's speed; uses FFI arrays under the LuaJIT JIT. " +
		"Its kernels are run as scimark-fft/-lu/-sor/-sparse"},
}

// vendored are the programs kept in testdata/bench/lua (see SOURCES.md
// there); the rest are fetched from the pinned commit.
var vendored = map[string]bool{"coroutine-ring.lua": true, "life.lua": true}

func (b *bench) script() string {
	if b.file != "" {
		return b.file
	}
	if name, ok := strings.CutPrefix(b.name, "moon/"); ok {
		return "moon_" + name + ".lua"
	}
	return b.name + ".lua"
}

func (b *bench) isMoon() bool { return strings.HasPrefix(b.name, "moon/") }

func (b *bench) upstream() bool {
	return !b.isMoon() && b.file == "" && !vendored[b.script()]
}

// workspace is the prepared work directory: every script, the wrapper and
// the inputs, mounted at the same path into the containers.
type workspace struct {
	dir   string // <cache>/work
	cache string
	root  string // the go-moonscript checkout
}

// prepare fills the work directory with the scripts of benches: the
// fetched suite files, the vendored ones, the compiled MoonScript
// benchmarks and moonscript/util.lua (which they require).
func (w *workspace) prepare(benches []*bench) error {
	if err := os.MkdirAll(filepath.Join(w.dir, "moonscript"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(w.dir, "out"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(w.dir, "wrap.lua"), wrapLua, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(w.dir, "empty.lua"), nil, 0o644); err != nil {
		return err
	}
	needUpstream := false
	for _, b := range benches {
		if b.upstream() {
			needUpstream = true
		}
	}
	var suiteDir string
	if needUpstream {
		var err error
		if suiteDir, err = fetchSuite(w.cache); err != nil {
			return err
		}
	}
	for _, b := range benches {
		switch {
		case b.isMoon():
			if err := w.compileMoon(b); err != nil {
				return err
			}
		case b.file != "":
		case vendored[b.script()]:
			src := filepath.Join(w.root, "testdata", "bench", "lua", b.script())
			if err := copyFile(src, filepath.Join(w.dir, b.script())); err != nil {
				return err
			}
			if suiteDir != "" {
				up, _ := os.ReadFile(filepath.Join(suiteDir, "bench", b.script()))
				own, _ := os.ReadFile(src)
				if !bytes.Equal(up, own) {
					fmt.Fprintf(os.Stderr, "luabench: warning: vendored %s differs from the suite's\n", b.script())
				}
			}
		default:
			for _, f := range append([]string{b.script()}, b.needs...) {
				if err := copyFile(filepath.Join(suiteDir, "bench", f), filepath.Join(w.dir, f)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// compileMoon compiles testdata/bench/moon/<name>.moon with our compiler
// (its output is byte-identical to the reference moonc's), for every
// engine; the moonscript.util it requires is our runtime's.
func (w *workspace) compileMoon(b *bench) error {
	name := strings.TrimPrefix(b.name, "moon/")
	src, err := os.ReadFile(filepath.Join(w.root, "testdata", "bench", "moon", name+".moon"))
	if err != nil {
		return err
	}
	code, _, err := moonscript.ToLua(string(src), moonscript.Options{})
	if err != nil {
		return fmt.Errorf("%s.moon: %w", name, err)
	}
	if err := os.WriteFile(filepath.Join(w.dir, b.script()), []byte(code), 0o644); err != nil {
		return err
	}
	return copyFile(filepath.Join(w.root, "runtime", "lua", "moonscript", "util.lua"),
		filepath.Join(w.dir, "moonscript", "util.lua"))
}

// fetchSuite returns a checkout of the suite at suiteCommit under cache,
// cloning it first if needed ($LUABENCH_REPO overrides the remote).
func fetchSuite(cache string) (string, error) {
	dir := filepath.Join(cache, "LuaJIT-test-cleanup-"+suiteCommit[:12])
	if head, err := gitOutput(dir, "rev-parse", "HEAD"); err == nil && head == suiteCommit {
		return dir, nil
	}
	repo := os.Getenv("LUABENCH_REPO")
	if repo == "" {
		repo = suiteRepo
	}
	tmp := dir + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return "", err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "luabench: fetching %s at %s\n", repo, suiteCommit[:12])
	for _, args := range [][]string{
		{"init", "-q"},
		{"fetch", "-q", "--depth", "1", repo, suiteCommit},
		{"-c", "advice.detachedHead=false", "checkout", "-q", "FETCH_HEAD"},
	} {
		if _, err := gitOutput(tmp, args...); err != nil {
			return "", err
		}
	}
	head, err := gitOutput(tmp, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if head != suiteCommit {
		return "", fmt.Errorf("fetched %s, want %s", head, suiteCommit)
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	return dir, os.Rename(tmp, dir)
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// inputFile returns the path of the input spec ("fasta:N", "sumcol:N"),
// generating it on first use (fasta.lua runs on gen, an engine).
func (w *workspace) inputFile(spec string, gen func(script, param, stdin, stdout string) error) (string, error) {
	kind, arg, _ := strings.Cut(spec, ":")
	n, err := strconv.ParseFloat(arg, 64)
	if err != nil {
		return "", fmt.Errorf("input %q: %w", spec, err)
	}
	path := filepath.Join(w.dir, "inputs", fmt.Sprintf("%s_%d", strings.ToUpper(kind), int(n)))
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	switch kind {
	case "fasta":
		if _, err := os.Stat(filepath.Join(w.dir, "fasta.lua")); err != nil {
			suiteDir, err := fetchSuite(w.cache)
			if err != nil {
				return "", err
			}
			if err := copyFile(filepath.Join(suiteDir, "bench", "fasta.lua"), filepath.Join(w.dir, "fasta.lua")); err != nil {
				return "", err
			}
		}
		if err := gen(filepath.Join(w.dir, "fasta.lua"), arg, "", tmp); err != nil {
			return "", fmt.Errorf("generating %s: %w", spec, err)
		}
	case "sumcol":
		suiteDir, err := fetchSuite(w.cache)
		if err != nil {
			return "", err
		}
		one, err := os.ReadFile(filepath.Join(suiteDir, "bench", "SUMCOL_1.txt"))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(tmp, bytes.Repeat(one, int(n)), 0o644); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unknown input kind %q", kind)
	}
	return path, os.Rename(tmp, path)
}
