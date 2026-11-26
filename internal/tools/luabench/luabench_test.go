package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMoonBenchmarks compiles the MoonScript benchmarks and runs them,
// small, on our VM's runner: their output must be PUC Lua 5.1.5's (recorded
// with luabench -engines puc and these parameters).
func TestMoonBenchmarks(t *testing.T) {
	want := map[string]struct{ param, out string }{
		"moon/classes":        {"100", "871200\t327200\t800\t267600\t10000\t10100\n"},
		"moon/comprehensions": {"10", "24200\n"},
		"moon/strings":        {"1000", "138698\talpha-1000: 2000 (ALPHA)\n"},
		"moon/destructure":    {"1000", "1275000\t2550000\t151000\t5405000\n"},
		"moon/compile":        {"10", "849.407464\t80550\n"},
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ws := &workspace{dir: filepath.Join(dir, "work"), cache: dir, root: root}
	var benches []*bench
	for i := range suite {
		if suite[i].isMoon() {
			benches = append(benches, &suite[i])
		}
	}
	if len(benches) != len(want) {
		t.Fatalf("%d MoonScript benchmarks, want %d", len(benches), len(want))
	}
	if err := ws.prepare(benches); err != nil {
		t.Fatal(err)
	}
	for _, b := range benches {
		t.Run(b.name, func(t *testing.T) {
			w := want[b.name]
			out := filepath.Join(dir, "stdout")
			if err := runCaptured(t, out, func() error {
				return runVM(filepath.Join(ws.dir, "wrap.lua"), []string{filepath.Join(ws.dir, b.script()), w.param}, os.Stderr)
			}); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(out)
			if string(got) != w.out {
				t.Errorf("output %q, want %q", got, w.out)
			}
		})
	}
}

// runCaptured runs f with os.Stdout redirected to a file (the io library
// of a new state writes to it).
func runCaptured(t *testing.T, stdout string, f func() error) error {
	t.Helper()
	of, err := os.Create(stdout)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = of
	defer func() {
		os.Stdout = old
		_ = of.Close()
	}()
	return f()
}
