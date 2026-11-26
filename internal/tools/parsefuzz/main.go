// Command parsefuzz is a differential fuzzer for the parser: it mutates the
// golden corpus (line deletions/duplications/swaps, indentation changes,
// token insertions, byte deletions, truncation, line-ending changes), parses
// every mutant with the reference implementation (moonref:0.8.0, one
// long-lived `lua5.1 scripts/refparse.lua serve` container per worker) and
// with the Go parser, and reports every difference in tree or error text.
//
// Mismatches are written to <out>/mismatch/NNNNN.{moon,ref,go}; with -min
// each one is also delta-debugged (lines, then bytes) to a small input that
// still mismatches, written as NNNNN.min.moon.
//
//	go run ./internal/tools/parsefuzz -n 5000 -min
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tarantool-contrib/go-moonscript/ast"
	"github.com/tarantool-contrib/go-moonscript/parse"
)

// result is what moonc -T would show: a tree (ok) or an error message.
type result struct {
	ok      bool
	text    string
	timeout bool
}

func (r result) String() string {
	if r.timeout {
		return "TIMEOUT"
	}
	if r.ok {
		return "TREE\n" + r.text
	}
	return "ERROR\n" + r.text
}

// --- reference server ------------------------------------------------------

type refServer struct {
	image, scripts string
	mode           string // "serve" or "serve-trace"
	timeout        time.Duration
	cmd            *exec.Cmd
	name           string
	in             io.WriteCloser
	out            *bufio.Reader
}

var containerSeq atomic.Int64

func (s *refServer) start() error {
	// named, so stop can kill the container itself: killing the docker
	// client leaves a container that is busy in a slow parse running
	s.name = fmt.Sprintf("parsefuzz-%d-%d", os.Getpid(), containerSeq.Add(1))
	s.cmd = exec.Command("docker", "run", "--rm", "-i", "--network=none", "--name", s.name,
		"-v", s.scripts+":/scripts:ro", s.image, "lua5.1", "/scripts/refparse.lua", s.mode)
	var err error
	if s.in, err = s.cmd.StdinPipe(); err != nil {
		return err
	}
	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	s.cmd.Stderr = os.Stderr
	s.out = bufio.NewReaderSize(stdout, 1<<20)
	return s.cmd.Start()
}

func (s *refServer) stop() {
	if s.cmd == nil {
		return
	}
	// best effort teardown: the server may be dead already, and its exit
	// status does not matter
	_ = s.in.Close()
	_ = exec.Command("docker", "kill", s.name).Run()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
	s.cmd = nil
}

func (s *refServer) parse(src string) (result, error) {
	if s.cmd == nil {
		if err := s.start(); err != nil {
			return result{}, err
		}
	}
	type reply struct {
		r   result
		err error
	}
	ch := make(chan reply, 1)
	go func() {
		if _, err := fmt.Fprintf(s.in, "%d\n%s", len(src), src); err != nil {
			ch <- reply{err: err}
			return
		}
		header, err := s.out.ReadString('\n')
		if err != nil {
			ch <- reply{err: err}
			return
		}
		kind, size, ok := strings.Cut(strings.TrimSuffix(header, "\n"), " ")
		n, err := strconv.Atoi(size)
		if !ok || err != nil {
			ch <- reply{err: fmt.Errorf("bad reply header %q", header)}
			return
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(s.out, buf); err != nil {
			ch <- reply{err: err}
			return
		}
		ch <- reply{r: result{ok: kind == "T", text: string(buf)}}
	}()
	select {
	case rep := <-ch:
		if rep.err != nil {
			s.stop()
		}
		return rep.r, rep.err
	case <-time.After(s.timeout):
		// the stop kills the container; the reader goroutine then fails
		s.stop()
		<-ch
		return result{timeout: true}, nil
	}
}

// --- Go side -----------------------------------------------------------------

var traceMode bool

func goParse(src string, timeout time.Duration) result {
	ch := make(chan result, 1)
	go func() {
		if traceMode {
			tr := parse.FurthestTrace(src)
			parts := make([]string, len(tr))
			for i, p := range tr {
				parts[i] = strconv.Itoa(p)
			}
			ch <- result{ok: true, text: strings.Join(parts, " ")}
			return
		}
		defer func() {
			if r := recover(); r != nil {
				ch <- result{text: fmt.Sprintf("PANIC: %v", r)}
			}
		}()
		tree, err := parse.String(src)
		if err != nil {
			ch <- result{text: err.Error()}
			return
		}
		ch <- result{ok: true, text: ast.Tree(tree)}
	}()
	select {
	case r := <-ch:
		return r
	case <-time.After(timeout):
		// the goroutine can't be stopped; it keeps running in the background
		return result{timeout: true}
	}
}

// --- mutations -----------------------------------------------------------------

var tokens = []string{
	"(", ")", "{", "}", "[", "]", ",", "=", "->", "=>", "\\", ".", ":", "!",
	"\"", "'", "[[", "]]", "[=[", "]=]", "#{", "@", "@@", "...", "--", ";",
	"#", "~", "|", "..", "!=", "+=", "or=", "+", "-", "*", "/", "<", ">",
	"\n", "\t", " ", "  ", "\r\n", "\r", "x", "1", "a.b", "f!",
	"do", "if", "then", "else", "elseif", "for", "in", "with", "switch",
	"when", "class", "extends", "return", "export", "local", "import",
	"from", "unless", "while", "and", "or", "not", "using", "nil", "break",
}

type mutator struct {
	rng    *rand.Rand
	maxMut int
}

func (m *mutator) pick(n int) int { return m.rng.Intn(n) }

func splitLines(s string) []string { return strings.SplitAfter(s, "\n") }

func (m *mutator) mutate(src string) string {
	lines := splitLines(src)
	join := func() string { return strings.Join(lines, "") }
	switch m.pick(13) {
	case 0: // delete a line
		i := m.pick(len(lines))
		lines = append(lines[:i:i], lines[i+1:]...)
		return join()
	case 1: // duplicate a line
		i := m.pick(len(lines))
		l := lines[i]
		if !strings.HasSuffix(l, "\n") {
			l += "\n"
		}
		lines = append(lines[:i:i], append([]string{l}, lines[i:]...)...)
		return join()
	case 2: // swap adjacent lines
		if len(lines) > 1 {
			i := m.pick(len(lines) - 1)
			lines[i], lines[i+1] = lines[i+1], lines[i]
		}
		return join()
	case 3: // change indentation of a line
		i := m.pick(len(lines))
		l := lines[i]
		trimmed := strings.TrimLeft(l, " \t")
		indent := l[:len(l)-len(trimmed)]
		switch m.pick(5) {
		case 0:
			indent += strings.Repeat(" ", 1+m.pick(4))
		case 1:
			if len(indent) > 0 {
				indent = indent[:len(indent)-1-m.pick(len(indent))]
			}
		case 2:
			indent = strings.ReplaceAll(indent, "    ", "\t")
			indent = strings.ReplaceAll(indent, "  ", "\t")
		case 3:
			indent = "\t" + indent
		default:
			indent = ""
		}
		lines[i] = indent + trimmed
		return join()
	case 4, 5, 6: // insert a token
		p := m.pick(len(src) + 1)
		t := tokens[m.pick(len(tokens))]
		if m.pick(2) == 0 {
			t = " " + t + " "
		}
		return src[:p] + t + src[p:]
	case 7: // delete a byte range
		if len(src) == 0 {
			return src
		}
		p := m.pick(len(src))
		n := 1 + m.pick(min(10, len(src)-p))
		return src[:p] + src[p+n:]
	case 8: // truncate
		return src[:m.pick(len(src)+1)]
	case 9: // replace a byte with a token
		if len(src) == 0 {
			return src
		}
		p := m.pick(len(src))
		return src[:p] + tokens[m.pick(len(tokens))] + src[p+1:]
	case 10: // join two lines
		if len(lines) > 1 {
			i := m.pick(len(lines) - 1)
			sep := []string{"", " ", " -- c "}[m.pick(3)]
			lines[i] = strings.TrimSuffix(lines[i], "\n") + sep
		}
		return join()
	case 11: // line endings / tabs for the whole input
		switch m.pick(3) {
		case 0:
			return strings.ReplaceAll(src, "\n", "\r\n")
		case 1:
			return strings.ReplaceAll(src, "  ", "\t")
		default:
			return strings.ReplaceAll(src, "\n", "\n\n")
		}
	default: // move a line to another position
		if len(lines) > 1 {
			i, j := m.pick(len(lines)), m.pick(len(lines))
			l := lines[i]
			lines = append(lines[:i:i], lines[i+1:]...)
			if j > len(lines) {
				j = len(lines)
			}
			lines = append(lines[:j:j], append([]string{l}, lines[j:]...)...)
		}
		return join()
	}
}

// sample picks a random window of a corpus file and mutates it 1-maxMut
// times.
func (m *mutator) sample(corpus []string) string {
	src := corpus[m.pick(len(corpus))]
	lines := splitLines(src)
	if len(lines) > 30 && m.pick(4) != 0 {
		n := 1 + m.pick(30)
		i := m.pick(len(lines) - n + 1)
		src = strings.Join(lines[i:i+n], "")
	}
	for k := 1 + m.pick(m.maxMut); k > 0; k-- {
		src = m.mutate(src)
	}
	return src
}

// --- minimization ----------------------------------------------------------

type checker struct {
	ref       *refServer
	goTimeout time.Duration
	budget    int // remaining tests for the current minimization
}

// mismatch reports whether src still shows a difference of the same kind
// (tree vs error) as the original mismatch.
func (c *checker) mismatch(src string, wantRefOK, wantGoOK bool) bool {
	if c.budget <= 0 {
		return false
	}
	c.budget--
	r, err := c.ref.parse(src)
	if err != nil || r.timeout || r.ok != wantRefOK {
		return false
	}
	g := goParse(src, c.goTimeout)
	if g.timeout || g.ok != wantGoOK {
		return false
	}
	return r.text != g.text
}

func ddmin(units []string, test func([]string) bool) []string {
	n := 2
	for len(units) >= 2 {
		chunk := (len(units) + n - 1) / n
		reduced := false
		for start := 0; start < len(units); start += chunk {
			end := min(start+chunk, len(units))
			cand := append(append([]string{}, units[:start]...), units[end:]...)
			if test(cand) {
				units = cand
				n = max(n-1, 2)
				reduced = true
				break
			}
		}
		if !reduced {
			if n >= len(units) {
				break
			}
			n = min(n*2, len(units))
		}
	}
	return units
}

func (c *checker) minimize(src string, refOK, goOK bool) string {
	c.budget = 3000
	test := func(u []string) bool { return c.mismatch(strings.Join(u, ""), refOK, goOK) }
	lines := ddmin(splitLines(src), test)
	src = strings.Join(lines, "")
	chars := strings.Split(src, "")
	return strings.Join(ddmin(chars, test), "")
}

// --- main ------------------------------------------------------------------------

func loadCorpus(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".moon") {
			files = append(files, path)
		}
		return err
	})
	sort.Strings(files)
	var corpus []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		corpus = append(corpus, string(b))
	}
	return corpus, err
}

func main() {
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = "."
	}
	corpusDir := flag.String("corpus", "testdata/parse", "corpus directory (*.moon, recursive)")
	n := flag.Int("n", 5000, "number of mutants")
	seed := flag.Int64("seed", 1, "random seed")
	workers := flag.Int("j", 4, "parallel workers (one reference container each)")
	out := flag.String("out", filepath.Join(cache, "go-moonscript", "parsefuzz"), "output directory")
	image := flag.String("image", "moonref:0.8.0", "reference docker image")
	scripts := flag.String("scripts", "scripts", "directory containing refparse.lua")
	timeout := flag.Duration("timeout", 5*time.Second, "per-case timeout (each side)")
	doMin := flag.Bool("min", false, "minimize mismatches")
	maxReport := flag.Int("max", 200, "stop collecting mismatches after this many")
	maxMut := flag.Int("k", 3, "maximum number of mutations per case")
	plain := flag.Bool("plain", false, "also run every corpus file unmodified")
	flag.BoolVar(&traceMode, "trace", false, "compare the sequence of furthest-failure increases "+
		"(reference: pure-Lua pgen parser) instead of trees/errors")
	flag.Parse()
	mode := "serve"
	if traceMode {
		mode = "serve-trace"
	}

	corpus, err := loadCorpus(*corpusDir)
	if err != nil || len(corpus) == 0 {
		log.Fatalf("loading corpus from %s: %v (%d files)", *corpusDir, err, len(corpus))
	}
	absScripts, err := filepath.Abs(*scripts)
	if err != nil {
		log.Fatal(err)
	}
	mdir := filepath.Join(*out, "mismatch")
	if err := os.MkdirAll(mdir, 0o755); err != nil {
		log.Fatal(err)
	}

	// mutants are generated up front from one RNG so runs are reproducible
	// regardless of -j
	mut := &mutator{rng: rand.New(rand.NewSource(*seed)), maxMut: max(*maxMut, 1)}
	var cases []string
	if *plain {
		cases = append(cases, corpus...)
	}
	for i := 0; i < *n; i++ {
		cases = append(cases, mut.sample(corpus))
	}

	var (
		next                                int64 = -1
		nDone, nMismatch, nRefTO, nGoTO, nE atomic.Int64
		mu                                  sync.Mutex
		mismatches                          []int
		wg                                  sync.WaitGroup
	)
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ref := &refServer{image: *image, scripts: absScripts, mode: mode, timeout: *timeout}
			defer ref.stop()
			// short timeouts while minimizing: candidates that get slow are
			// simply not taken
			chk := &checker{ref: &refServer{image: *image, scripts: absScripts, mode: mode, timeout: time.Second},
				goTimeout: time.Second}
			defer chk.ref.stop()
			for {
				i := int(atomic.AddInt64(&next, 1))
				if i >= len(cases) || nMismatch.Load() >= int64(*maxReport) {
					return
				}
				src := cases[i]
				r, err := ref.parse(src)
				if err != nil {
					log.Printf("case %d: reference: %v", i, err)
					continue
				}
				done := nDone.Add(1)
				if done%1000 == 0 {
					log.Printf("%d/%d done, %d mismatches", done, len(cases), nMismatch.Load())
				}
				if r.timeout {
					nRefTO.Add(1)
					tdir := filepath.Join(*out, "timeout")
					if err := os.MkdirAll(tdir, 0o755); err != nil {
						log.Fatal(err)
					}
					mustWrite(filepath.Join(tdir, fmt.Sprintf("%05d.moon", i)), src)
					continue
				}
				if !r.ok {
					nE.Add(1)
				}
				g := goParse(src, *timeout)
				if g.timeout {
					nGoTO.Add(1)
				}
				if g.timeout || g.ok != r.ok || g.text != r.text {
					nMismatch.Add(1)
					base := filepath.Join(mdir, fmt.Sprintf("%05d", i))
					mustWrite(base+".moon", src)
					mustWrite(base+".ref", r.String()+"\n")
					mustWrite(base+".go", g.String()+"\n")
					if *doMin && !g.timeout {
						small := chk.minimize(src, r.ok, g.ok)
						mustWrite(base+".min.moon", small)
						r2, _ := ref.parse(small)
						g2 := goParse(small, *timeout)
						mustWrite(base+".min.ref", r2.String()+"\n")
						mustWrite(base+".min.go", g2.String()+"\n")
					}
					mu.Lock()
					mismatches = append(mismatches, i)
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	sort.Ints(mismatches)
	fmt.Printf("cases: %d run (%d reference errors, %d reference timeouts, %d go timeouts)\n",
		nDone.Load(), nE.Load(), nRefTO.Load(), nGoTO.Load())
	fmt.Printf("mismatches: %d (in %s)\n", len(mismatches), mdir)
	for _, i := range mismatches {
		fmt.Printf("  %05d\n", i)
	}
	if len(mismatches) > 0 {
		os.Exit(1)
	}
}

// mustWrite saves a case file; the run is useless if its cases are lost.
func mustWrite(path, data string) {
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		log.Fatal(err)
	}
}
