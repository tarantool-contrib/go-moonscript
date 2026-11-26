//go:build linux

package luasuites

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The reference interpreters, whose outcomes scripts/gen-luasuites-ref.sh
// records in testdata/ref/<interp>/<suite>.txt.
const (
	refLua51  = "lua5.1"
	refLuaJIT = "luajit"
)

// refRun is the recorded run of a unit by a reference interpreter: its exit
// status and the lines of its standard output the suite looks at.
type refRun struct {
	exit  int
	lines []string
}

// ended describes how a reference run ended that did not exit with
// status 0, like result.ended (timeout(1) exits with 124).
func (r *refRun) ended() string {
	if r.exit == 124 {
		return "timeout"
	}
	return fmt.Sprintf("exit %d", r.exit)
}

// loadRefs reads testdata/ref/<interp>/<suite>.txt: blocks of
//
//	### <unit> exit=<status>
//	<lines>
func loadRefs(t *testing.T, interp, suite string) map[string]*refRun {
	t.Helper()
	path := filepath.Join("testdata", "ref", interp, suite+".txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("%v (run scripts/gen-luasuites-ref.sh)", err)
	}
	defer func() { _ = f.Close() }()
	refs := map[string]*refRun{}
	var cur *refRun
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, "### "); ok {
			i := strings.LastIndex(rest, " exit=")
			if i < 0 {
				t.Fatalf("%s: bad header %q", path, line)
			}
			exit, err := strconv.Atoi(rest[i+len(" exit="):])
			if err != nil {
				t.Fatalf("%s: bad header %q", path, line)
			}
			cur = &refRun{exit: exit}
			refs[rest[:i]] = cur
			continue
		}
		if cur == nil {
			t.Fatalf("%s: line before the first header", path)
		}
		cur.lines = append(cur.lines, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return refs
}

// refOf returns the reference run of a unit, failing the test if there is
// none.
func refOf(t *testing.T, refs map[string]*refRun, interp, unit string) *refRun {
	t.Helper()
	r := refs[unit]
	if r == nil {
		t.Errorf("%s: no %s reference (run scripts/gen-luasuites-ref.sh)", unit, interp)
		return &refRun{exit: -1}
	}
	return r
}

// skips are the units testdata/skip.tsv excludes, with the reasons:
// suite -> unit -> reason.
func loadSkips(t *testing.T) map[string]map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "skip.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	skips := map[string]map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			t.Fatalf("skip.tsv: bad line %q", line)
		}
		if skips[f[0]] == nil {
			skips[f[0]] = map[string]string{}
		}
		skips[f[0]][f[1]] = f[2]
	}
	return skips
}
