package main

// Golden tests for moonc -l: testdata/lint/COMMANDS.tsv lists moonc
// invocations, testdata/lint/NAME.out holds the reference moonc's exit
// status and stderr (regenerate with scripts/gen-lint-golden.sh).

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lint"
)

func TestLintGolden(t *testing.T) {
	dir, err := filepath.Abs("../../testdata/lint")
	if err != nil {
		t.Fatal(err)
	}
	cmds, err := os.ReadFile(filepath.Join(dir, "COMMANDS.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(cmds), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			t.Fatalf("bad COMMANDS.tsv line %q", line)
		}
		name, sub, argLine := fields[0], fields[1], fields[2]
		n++
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(dir, name+".out"))
			if err != nil {
				t.Fatal(err)
			}
			head, wantStderr, _ := strings.Cut(string(want), "\n")
			wantCode, err := strconv.Atoi(strings.TrimPrefix(head, "exit "))
			if err != nil {
				t.Fatalf("bad status line %q", head)
			}

			t.Chdir(filepath.Join(dir, sub))
			lint.ResetConfig()
			var args []string
			var stdin []byte
			for _, a := range strings.Fields(argLine) {
				if file, ok := strings.CutPrefix(a, "<"); ok {
					if stdin, err = os.ReadFile(file); err != nil {
						t.Fatal(err)
					}
					continue
				}
				args = append(args, a)
			}
			opts := parseArgs("moonc", args)
			if !opts.flags["lint"] {
				t.Fatalf("%q is not a lint command", argLine)
			}
			var stderr bytes.Buffer
			var code int
			if opts.flags["stdin"] {
				code = lintStdin(string(stdin), &stderr, lintOptions(opts))
			} else {
				code = lintFiles(collectFiles(opts), &stderr, lintOptions(opts))
			}
			if code != wantCode {
				t.Errorf("exit status %d, want %d", code, wantCode)
			}
			if got := stderr.String(); got != wantStderr {
				t.Errorf("stderr mismatch\n--- want ---\n%s\n--- got ---\n%s", wantStderr, got)
			}
		})
	}
	if n == 0 {
		t.Fatal("no commands")
	}
}
