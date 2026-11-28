package parse

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
)

// outcome renders a parse result (tree with positions, error message or
// panic) for comparison.
func outcome(parse func(string) (*ast.Node, error), src string) (s string) {
	defer func() {
		if r := recover(); r != nil {
			s = fmt.Sprintf("PANIC: %v", r)
		}
	}()
	tree, err := parse(src)
	if err != nil {
		return "ERROR\n" + err.Error()
	}
	return "TREE\n" + ast.Tree(tree)
}

func interpString(src string) (*ast.Node, error) {
	return stringWith(src, interpreter().match)
}

// checkDifferential compares the generated parser with the interpreter of
// the grammar description on src: results and furthest-failure traces.
// The generated parser runs in both packrat modes (see packratEager).
func checkDifferential(t *testing.T, name, src string) {
	t.Helper()
	want, wantTrace := outcome(interpString, src), interpreter().trace(src)
	defer func() { packratEager = false }()
	for _, eager := range []bool{false, true} {
		packratEager = eager
		if got := outcome(String, src); got != want {
			t.Errorf("%s (eager memo %v): generated parser differs from the interpreter\n--- generated:\n%s\n--- interpreter:\n%s",
				name, eager, clip(got), clip(want))
			return
		}
		if gt := FurthestTrace(src); !slices.Equal(gt, wantTrace) {
			t.Errorf("%s (eager memo %v): furthest-failure trace differs\n--- generated: %v\n--- interpreter: %v",
				name, eager, clipInts(gt), clipInts(wantTrace))
			return
		}
	}
}

func clip(s string) string {
	if len(s) > 2000 {
		return s[:2000] + "..."
	}
	return s
}

func clipInts(v []int) []int {
	if len(v) > 50 {
		return v[:50]
	}
	return v
}

func diffInputs(t testing.TB) map[string]string {
	inputs := map[string]string{}
	dirs := []string{"../testdata/parse"}
	if d := os.Getenv("MOON_DIFF_CORPUS"); d != "" {
		dirs = append(dirs, filepath.SplitList(d)...)
	}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".moon") {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				inputs[path] = string(b)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return inputs
}

// TestDifferential runs the generated parser and the interpreter on the
// golden inputs (and on the *.moon files under $MOON_DIFF_CORPUS, a
// list of directories) plus variants that stress the depth limit.
func TestDifferential(t *testing.T) {
	inputs := diffInputs(t)
	if len(inputs) == 0 {
		t.Fatal("no inputs")
	}
	for name, src := range inputs {
		checkDifferential(t, name, src)
	}
	// around the recursion depth limit: find where each family of nested
	// inputs starts to hit it, then compare both sides of the threshold
	// (families whose backtracking is exponential, like nested parentheses
	// or interpolations, cannot get there in reasonable time)
	families := map[string]func(n int) string{
		"calls":  func(n int) string { return "x = " + strings.Repeat("f(", n) + "1" + strings.Repeat(")", n) },
		"not":    func(n int) string { return "x = " + strings.Repeat("not ", n) + "1" },
		"length": func(n int) string { return "x = " + strings.Repeat("#", n) + "y" },
		"args":   func(n int) string { return strings.Repeat("f ", n) + "1" },
		// an Exp tried as a table comprehension first, then deeper as a
		// table item: memo hits deeper than the stored evaluation
		"table":  func(n int) string { return "x = {" + strings.Repeat("f(", n) + "1" + strings.Repeat(")", n) + "}" },
		"tables": func(n int) string { return "x = {{{" + strings.Repeat("not ", n) + "1}}}" },
		"comp":   func(n int) string { return "x = [" + strings.Repeat("#", n) + "y]" },
		"ifs":    nestedIfs,
		"fns":    nestedFns,
	}
	for name, gen := range families {
		lo, hi := 1, 1
		for !isDepthError(gen(hi)) {
			lo, hi = hi, hi*2
			if hi > 1<<14 {
				t.Fatalf("%s: no depth error up to %d", name, hi)
			}
		}
		for hi-lo > 1 {
			mid := (lo + hi) / 2
			if isDepthError(gen(mid)) {
				hi = mid
			} else {
				lo = mid
			}
		}
		for _, n := range []int{lo - 1, lo, hi, hi + 1} {
			checkDifferential(t, fmt.Sprintf("%s%d", name, n), gen(n))
		}
		checkDifferential(t, fmt.Sprintf("%s%d-broken", name, lo), gen(lo)+"\n)")
		checkDifferential(t, fmt.Sprintf("%s%d-broken", name, hi), gen(hi)+"\n)")
	}
}

func isDepthError(src string) bool {
	_, err := String(src)
	return err != nil && strings.Contains(err.Error(), "max recursion depth")
}

func nestedFns(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(strings.Repeat("\t", i) + "f = ->\n")
	}
	b.WriteString(strings.Repeat("\t", n) + "y!\n")
	return b.String()
}

func nestedIfs(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(strings.Repeat(" ", i) + "if x\n")
	}
	b.WriteString(strings.Repeat(" ", n) + "y!\n")
	return b.String()
}

// FuzzDifferential compares the generated parser with the interpreter on
// arbitrary inputs:
//
//	go test ./moonscript/parse -run XXX -fuzz FuzzDifferential
func FuzzDifferential(f *testing.F) {
	for _, src := range diffInputs(f) {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		checkDifferential(t, "input", src)
	})
}

// TestConcurrentParses checks that parses sharing pooled parsers do not
// interfere (run with -race).
func TestConcurrentParses(t *testing.T) {
	inputs := diffInputs(t)
	want := map[string]string{}
	for name, src := range inputs {
		want[name] = outcome(String, src)
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name, src := range inputs {
				if got := outcome(String, src); got != want[name] {
					t.Errorf("%s: concurrent parse differs", name)
				}
			}
		}()
	}
	wg.Wait()
}
