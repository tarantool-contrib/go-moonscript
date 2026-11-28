package flow_test

// luacheck is the oracle of the lint rules that it has: TestLuacheck runs
// it over the fork's testdata (the compatibility scripts among them), the
// luasuites archives and random programs (genProgram), and compares its
// warnings with those of luacheckLint over the Programs, per file, line,
// column and code. It needs docker and an image whose entry point is
// luacheck; dir is where the sources are written for it:
//
//	FLOW_LUACHECK_IMAGE=image FLOW_LUACHECK_DIR=dir go test -run TestLuacheck ./lua/flow
//
// A Debian image with lua-check will do:
//
//	FROM debian:stable-slim
//	RUN apt-get update && apt-get install -y --no-install-recommends lua-check
//	ENTRYPOINT ["luacheck"]
//
// A difference must be one of luacheck's approximations that quirks
// reproduces (knownDifference); any other fails the test.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tarantool-contrib/go-moonscript/lua/flow"
)

// genCount is the number of random programs (genProgram) compared, unless
// FLOW_LUACHECK_GEN gives another.
const genCount = 4000

var luacheckCodes = []string{"511", "512", "541", "542", "311", "312", "313", "321", "341"}

type finding struct {
	file      string
	line, col int
	code      string
}

func (f finding) String() string { return fmt.Sprintf("%s:%d:%d: W%s", f.file, f.line, f.col, f.code) }

var luacheckLine = regexp.MustCompile(`^(.*):(\d+):(\d+)-(\d+): \(([EW])(\d+)\) (.*)$`)

func TestLuacheck(t *testing.T) {
	image, dir := os.Getenv("FLOW_LUACHECK_IMAGE"), os.Getenv("FLOW_LUACHECK_DIR")
	if image == "" || dir == "" {
		t.Skip("FLOW_LUACHECK_IMAGE and FLOW_LUACHECK_DIR are not set")
	}
	files := corpus(t)
	n := genCount
	if s := os.Getenv("FLOW_LUACHECK_GEN"); s != "" {
		var err error
		if n, err = strconv.Atoi(s); err != nil {
			t.Fatal("FLOW_LUACHECK_GEN:", err)
		}
	}
	for i := range n {
		files[fmt.Sprintf("gen/%05d.lua", i)] = []byte(genProgram(uint64(i), i%2 == 0))
	}
	// luacheck has no option to ignore the sources' inline options ("--
	// luacheck: ignore"): they are disabled in the copies, keeping the
	// offsets
	for name, b := range files {
		files[name] = bytes.ReplaceAll(b, []byte("luacheck:"), []byte("luacheck_"))
	}
	srcDir := filepath.Join(dir, "src")
	if err := os.RemoveAll(srcDir); err != nil {
		t.Fatal(err)
	}
	// the path of each source under srcDir: luacheck takes the .lua files
	paths := map[string]string{}
	for name, b := range files {
		p := name
		if !strings.HasSuffix(p, ".lua") {
			p += ".lua"
		}
		paths[p] = name
		full := filepath.Join(srcDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	abs, err := filepath.Abs(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	args := []string{"run", "--rm", "-v", abs + ":/src:ro", "-w", "/src", image,
		"--codes", "--ranges", "--formatter", "plain", "--no-config", "--std", "max", "--only"}
	args = append(args, luacheckCodes...)
	args = append(args, "--", ".")
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		t.Fatalf("luacheck: %v: %s", err, stderr.String())
	}
	theirs := map[finding]bool{}
	bad := map[string]bool{} // files luacheck can't parse
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		m := luacheckLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		file := paths[strings.TrimPrefix(m[1], "./")]
		if m[5] == "E" {
			bad[file] = true
			continue
		}
		line, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		theirs[finding{file, line, col, m[6]}] = true
	}
	ours := map[finding]bool{}
	// the findings with luacheck's quirks
	compat := make([]map[finding]bool, len(quirks))
	for i := range compat {
		compat[i] = map[finding]bool{}
	}
	skipped := 0
	for _, name := range sortedKeys(files) {
		if bad[name] {
			skipped++
			continue
		}
		chunk, info, _, ok := parseAny(files[name], name)
		if !ok {
			skipped++
			for f := range theirs {
				if f.file == name {
					delete(theirs, f)
				}
			}
			continue
		}
		for _, w := range luacheckLint(chunk, info, flow.Analyze(info, nil), false) {
			ours[finding{name, w.pos.Line, w.pos.Column, w.code}] = true
		}
		for i, q := range quirks {
			opts := flow.LuacheckOptions(q.closures, q.parens, q.scopes, q.circular)
			for _, w := range luacheckLint(chunk, info, flow.Analyze(info, opts), q.parens) {
				compat[i][finding{name, w.pos.Line, w.pos.Column, w.code}] = true
			}
		}
	}
	for f := range theirs {
		if bad[f.file] {
			delete(theirs, f)
		}
	}
	// the counts per part of the corpus and code
	type count struct{ both, onlyTheirs, onlyOurs, explained int }
	parts := []string{"testdata and archives", "random programs"}
	part := func(f finding) int {
		if strings.HasPrefix(f.file, "gen/") {
			return 1
		}
		return 0
	}
	counts := map[[2]string]*count{}
	for _, p := range parts {
		for _, c := range luacheckCodes {
			counts[[2]string{p, c}] = &count{}
		}
	}
	at := func(f finding) *count { return counts[[2]string{parts[part(f)], f.code}] }
	var unexplained []string
	check := func(f finding, side string) {
		c := at(f)
		if why := knownDifference(f, side, compat); why != "" {
			c.explained++
			t.Logf("explained: %s only in %s: %s", f, side, why)
			return
		}
		unexplained = append(unexplained, fmt.Sprintf("%s only in %s", f, side))
	}
	for f := range theirs {
		if ours[f] {
			at(f).both++
		} else {
			at(f).onlyTheirs++
			check(f, "luacheck")
		}
	}
	for f := range ours {
		if !theirs[f] {
			at(f).onlyOurs++
			check(f, "flow")
		}
	}
	t.Logf("%d files, %d skipped (no parse)", len(files), skipped)
	for _, p := range parts {
		for _, c := range luacheckCodes {
			n := counts[[2]string{p, c}]
			t.Logf("%s: W%s: %d agree, %d only luacheck, %d only flow, %d of them explained", p, c, n.both, n.onlyTheirs, n.onlyOurs, n.explained)
		}
	}
	sort.Strings(unexplained)
	for i, u := range unexplained {
		if i == 200 {
			t.Errorf("... and %d more", len(unexplained)-i)
			break
		}
		t.Error(u)
	}
}

// quirks are the approximations of luacheck that the package can reproduce
// (flow.LuacheckOptions), to explain the differences with it.
var quirks = []struct {
	closures, parens, scopes, circular bool
	why                                string
}{
	// luacheck cross-resolves the closures inside a closure at each level
	// above it: for a local of a function other than the main chunk, every
	// value a closure assigns is seen by every closure, also by those that
	// never share an instance of the variable (in branches that exclude
	// each other, or created after the function returned)
	{true, false, false, false, "luacheck connects all the closures of a local"},
	// luacheck keeps the parentheses of the last value of an assignment
	// ("x = (function() end)"): to it the value is no function, so the
	// function counts as used by the function it is in (W311 of unused
	// recursive functions)
	{false, true, false, false, "luacheck takes a parenthesized function value as no function"},
	// luacheck ends the scope of a variable at a declaration of the same
	// name in the same scope ("local x = 1 ... local x"): a goto back to
	// before the declaration doesn't take the values back to the uses
	// there
	{false, false, true, false, "luacheck ends a scope at a redeclaration"},
	// luacheck doesn't connect the reads of t in the functions that a
	// circular assignment of t creates ("t.f = function() return t end",
	// "function t:m()") with the values reaching the assignment: it sees
	// no uninitialized read there, or only the values of other closures
	{false, false, false, true, "luacheck ignores the values reaching a circular assignment's closures"},
	{true, true, true, true, "luacheck's four quirks"},
}

// knownDifference explains a difference with luacheck, or returns "": a
// difference that one of luacheck's quirks, reproduced, makes go away.
func knownDifference(f finding, side string, compat []map[finding]bool) string {
	for i, q := range quirks {
		if compat[i][f] == (side == "luacheck") {
			return q.why
		}
	}
	return ""
}
