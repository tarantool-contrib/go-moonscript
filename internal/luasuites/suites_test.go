//go:build linux

package luasuites

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	lua "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua"
)

func TestMain(m *testing.M) {
	if progname, ok := os.LookupEnv(childEnv); ok {
		os.Exit(luaMain(progname, os.Args[1:]))
	}
	code := m.Run()
	if ws != nil {
		_ = os.RemoveAll(ws.dir)
	}
	os.Exit(code)
}

// full is set by LUASUITES_FULL=1: run the slow units too (slowUnits).
var full = os.Getenv("LUASUITES_FULL") != ""

// slowUnits take long on our VM; they run with LUASUITES_FULL=1 only, and
// always in update mode, with the time limit given here.
var slowUnits = map[string]time.Duration{
	// compiles a function with 30000 constants: about 110 s, quadratic
	"lua5.1-tests/verybig.lua": 600 * time.Second,
}

func runAll() bool { return full || updating() }

func TestSuites(t *testing.T) {
	w := getWorkspace(t)
	skips := loadSkips(t)
	t.Run("group", func(t *testing.T) {
		t.Run("lua5.1-tests", func(t *testing.T) { t.Parallel(); checkLedger(t, lua51Tests(t, w, skips)) })
		t.Run("harness", func(t *testing.T) {
			t.Parallel()
			res, jit := harnessTests(t, w, skips)
			checkLedger(t, res)
			checkLedger(t, jit)
		})
		t.Run("luajit-tests", func(t *testing.T) { t.Parallel(); checkLedger(t, luajitTests(t, w, skips)) })
		t.Run("parse", func(t *testing.T) { t.Parallel(); checkLedger(t, parseTests(t, w)) })
	})
	if updating() && !t.Failed() {
		writeLedger(t)
	}
}

// forEach runs fn for every unit concurrently (the interpreter processes
// are limited by slots).
func forEach(units []string, fn func(unit string)) {
	var wg sync.WaitGroup
	for _, u := range units {
		wg.Go(func() { fn(u) })
	}
	wg.Wait()
}

// testdataPath is the absolute path of a file in testdata.
func testdataPath(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// refStatus is the one-word status of a reference outcome.
func refStatus(outcome string) string {
	s, _, _ := strings.Cut(outcome, " ")
	return strings.TrimSuffix(s, ":")
}

// --- lua5.1-tests -----------------------------------------------------------

var reRun51 = regexp.MustCompile(`^(RESULT|NOTE)\t`)

// outcome51 is the outcome of a file of lua5.1-tests from the lines
// run51.lua printed ("pass", "fail <where>: <message>" or how the run
// ended), and its notes (Message calls: the parts the file skipped).
func outcome51(lines []string, ended string) (outcome, notes string) {
	var ns []string
	outcome = ended
	for _, l := range lines {
		f := strings.SplitN(l, "\t", 4)
		switch {
		case f[0] == "NOTE":
			ns = append(ns, normalize(strings.Join(f[1:], " ")))
		case len(f) >= 2 && f[1] == "pass":
			outcome = "pass"
		case len(f) == 4:
			// the message without the position when it repeats it
			loc := normalize(f[2])
			outcome = "fail " + loc + ": " + strings.TrimPrefix(normalize(f[3]), loc+": ")
		}
	}
	return outcome, strings.Join(ns, "; ")
}

func lua51Tests(t *testing.T, w *workspace, skips map[string]map[string]string) suiteResult {
	const suite = "lua5.1-tests"
	dir := filepath.Join(w.dir, suite, suite)
	driver := testdataPath(t, "lua/run51.lua")
	refs := map[string]map[string]*refRun{}
	for _, interp := range []string{refLua51, refLuaJIT} {
		refs[interp] = loadRefs(t, interp, suite)
	}
	files := []string{"--setup"} // all.lua's setup, see run51.lua
	for _, f := range w.files[suite] {
		if d, name := path.Split(f.name); d == suite+"/" && strings.HasSuffix(name, ".lua") {
			files = append(files, name)
		}
	}
	res := suiteResult{suite: suite}
	var mu sync.Mutex
	var units []string
	stats := map[string]int{}
	for _, f := range files {
		if reason, ok := skips[suite][f]; ok {
			res.units = append(res.units, f)
			res.rows = append(res.rows, row{suite, f, "-", "skip: " + reason})
			continue
		}
		if _, slow := slowUnits[suite+"/"+f]; slow && !runAll() {
			continue
		}
		units = append(units, f)
	}
	forEach(units, func(f string) {
		pdir, err := w.privateDir(dir, f)
		if err != nil {
			t.Error(err)
			return
		}
		limit := timeout
		if d, ok := slowUnits[suite+"/"+f]; ok {
			limit = d
		}
		r := w.run(pdir, limit, "?;./?.lua", driver, f)
		ended := "no result"
		if r.exit != 0 || r.timedOut {
			ended = r.ended()
		}
		ours, notes := outcome51(keepLines(r.stdout, reRun51), ended)
		var refOutcome [2]string
		var refNotes string
		for i, interp := range []string{refLua51, refLuaJIT} {
			rr := refOf(t, refs[interp], interp, f)
			ended := "no result"
			if rr.exit != 0 {
				ended = rr.ended()
			}
			var n string
			refOutcome[i], n = outcome51(rr.lines, ended)
			if i == 0 {
				refNotes = n
			}
		}
		mu.Lock()
		defer mu.Unlock()
		res.units = append(res.units, f)
		outcome := ours
		if ours != "pass" {
			outcome += fmt.Sprintf(" [lua5.1: %s, luajit: %s]", refStatus(refOutcome[0]), refStatus(refOutcome[1]))
		}
		res.rows = append(res.rows, row{suite, f, "-", outcome})
		if ours == "pass" && notes != refNotes {
			res.rows = append(res.rows, row{suite, f, "notes", fmt.Sprintf("ours: %q; lua5.1: %q", notes, refNotes)})
		}
		stats["files"]++
		for i, o := range []string{ours, refOutcome[0], refOutcome[1]} {
			if o == "pass" {
				stats[[]string{"ours", "lua5.1", "luajit"}[i]]++
			}
		}
		if ours == "pass" && refOutcome[0] == "pass" {
			stats["both"]++
		}
	})
	t.Logf("summary: lua5.1-tests: %d files run, %d skipped; pass: ours %d, lua5.1 %d, luajit %d",
		stats["files"], len(res.units)-stats["files"], stats["ours"], stats["lua5.1"], stats["luajit"])
	return res
}

// --- lua-Harness-tests ------------------------------------------------------

// reFloat matches the numbers with many decimals in diagnostics, which
// may vary from run to run (random numbers, timings).
var reFloat = regexp.MustCompile(`\d+\.\d{5,}(e[-+]?\d+)?`)

var reTAP = regexp.MustCompile(`^(not )?ok (\d+)(.*)$|^1\.\.\d+|^Bail out!`)

// tapResult is a parsed TAP stream: the plan and each test's status ("ok",
// "not ok", "skip", "todo ok", "todo not ok") and description.
type tapResult struct {
	plan   string
	status map[int]string
	desc   map[int]string
	diag   map[int][]string // the diagnostics after a test (ours only)
	max    int
}

func parseTAP(lines []string) *tapResult {
	tr := &tapResult{status: map[int]string{}, desc: map[int]string{}, diag: map[int][]string{}}
	last := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "1..") {
			tr.plan = l
			continue
		}
		if strings.HasPrefix(l, "#") {
			if last > 0 && len(tr.diag[last]) < 2 {
				d := reFloat.ReplaceAllString(strings.TrimPrefix(l, "#"), "<float>")
				tr.diag[last] = append(tr.diag[last], normalize(d))
			}
			continue
		}
		m := reTAP.FindStringSubmatch(l)
		if m == nil || m[2] == "" {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		status := "ok"
		if m[1] != "" {
			status = "not ok"
		}
		desc := strings.TrimPrefix(strings.TrimSpace(m[3]), "- ")
		if d, reason, ok := strings.Cut(desc, "# TODO"); ok {
			status, desc = "todo "+status, strings.TrimSpace(d)+" (TODO"+reason+")"
		} else if strings.HasPrefix(desc, "# skip") {
			status = "skip"
		}
		tr.status[n], tr.desc[n] = status, normalize(desc)
		tr.max = max(tr.max, n)
		last = n
	}
	return tr
}

var reTAPOrDiag = regexp.MustCompile(`^(not )?ok \d+|^1\.\.\d+|^Bail out!|^#`)

// compareTAP gives the rows of a TAP file where ours differs from the
// reference interp: a row for the file if the plan or the exit differ, one
// per differing test, and one per run of tests ours is missing.
func compareTAP(suite, unit, interp string, ours *result, ref *refRun) (rows []row, same, total int) {
	o := parseTAP(keepLines(ours.stdout, reTAPOrDiag))
	r := parseTAP(ref.lines)
	oursEnd := "exit 0"
	if ours.exit != 0 || ours.timedOut {
		oursEnd = ours.ended()
	}
	refEnd := "exit 0"
	if ref.exit != 0 {
		refEnd = ref.ended()
	}
	if o.plan != r.plan || refStatus(oursEnd) != refStatus(refEnd) || (ours.exit != 0) != (ref.exit != 0) {
		rows = append(rows, row{suite, unit, "-", fmt.Sprintf("plan %q, %s [%s: plan %q, %s]",
			o.plan, normalize(oursEnd), interp, r.plan, refEnd)})
	}
	n := max(o.max, r.max)
	missing := 0
	flush := func(i int) {
		if missing > 0 {
			rows = append(rows, row{suite, unit, fmt.Sprintf("#%d-#%d", i-missing, i-1), "missing"})
			missing = 0
		}
	}
	for i := 1; i <= n; i++ {
		st, rs := o.status[i], r.status[i]
		if rs != "" {
			total++
		}
		if st == rs {
			same++
			flush(i)
			continue
		}
		if st == "" {
			missing++
			continue
		}
		flush(i)
		if rs == "" {
			rs = "missing"
		}
		desc := r.desc[i]
		if desc == "" {
			desc = o.desc[i]
		}
		out := fmt.Sprintf("%s [%s: %s] %s", st, interp, rs, desc)
		if d := o.diag[i]; len(d) > 0 {
			out += " | " + strings.Join(d, " | ")
		}
		rows = append(rows, row{suite, unit, fmt.Sprintf("#%d", i), out})
	}
	flush(n + 1)
	return rows, same, total
}

func harnessTests(t *testing.T, w *workspace, skips map[string]map[string]string) (suiteResult, suiteResult) {
	const dirName = "test/lua-Harness-tests/"
	dir := filepath.Join(w.dir, "luajit-tests", dirName)
	luaPath := "./?.lua;" + testdataPath(t, "lua") + "/?.lua;;"
	views := []struct {
		suite, interp string
		dialect       lua.Dialect
		args          []string
	}{
		{"harness", refLua51, lua.DialectLua51, []string{"-l", "profile_lua51"}},
		{"harness-jit", refLuaJIT, lua.DialectLuaJIT, []string{"-l", "profile_luajit21"}},
	}
	refs := map[string]map[string]*refRun{}
	for _, interp := range []string{refLua51, refLuaJIT} {
		refs[interp] = loadRefs(t, interp, "harness")
	}
	var files []string
	for _, f := range w.files["luajit-tests"] {
		if d, name := path.Split(f.name); d == dirName && strings.HasSuffix(name, ".t") {
			files = append(files, name)
		}
	}
	results := []suiteResult{{suite: views[0].suite}, {suite: views[1].suite}}
	var mu sync.Mutex
	var jobs []string
	stats := map[string][3]int{} // view -> files same, assertions same, total
	for vi, v := range views {
		for _, f := range files {
			if reason, ok := skips[v.suite][f]; ok {
				results[vi].units = append(results[vi].units, f)
				results[vi].rows = append(results[vi].rows, row{v.suite, f, "-", "skip: " + reason})
				continue
			}
			jobs = append(jobs, fmt.Sprint(vi)+"\t"+f)
		}
	}
	forEach(jobs, func(j string) {
		vs, f, _ := strings.Cut(j, "\t")
		vi := int(vs[0] - '0')
		v := views[vi]
		pdir, err := w.privateDir(dir, v.suite+"_"+f)
		if err != nil {
			t.Error(err)
			return
		}
		ours := w.runDialect(v.dialect, pdir, timeout, luaPath, append(slices.Clone(v.args), f)...)
		ref := refOf(t, refs[v.interp], v.interp, f)
		rows, same, total := compareTAP(v.suite, f, v.interp, ours, ref)
		mu.Lock()
		defer mu.Unlock()
		results[vi].units = append(results[vi].units, f)
		results[vi].rows = append(results[vi].rows, rows...)
		s := stats[v.suite]
		if len(rows) == 0 {
			s[0]++
		}
		s[1] += same
		s[2] += total
		stats[v.suite] = s
	})
	// the LuaJIT view keeps the LuaJIT-only differences: a row the 5.1 view
	// has too (same test, same result, same reference result) goes
	sharedKey := func(r row, interp string) string {
		return r.unit + "\t" + r.item + "\t" + strings.Replace(r.outcome, "["+interp+":", "[ref:", 1)
	}
	shared := map[string]bool{}
	for _, r := range results[0].rows {
		shared[sharedKey(r, views[0].interp)] = true
	}
	jitRows := results[1].rows[:0]
	for _, r := range results[1].rows {
		if strings.HasPrefix(r.outcome, "skip: ") || !shared[sharedKey(r, views[1].interp)] {
			jitRows = append(jitRows, r)
		}
	}
	results[1].rows = jitRows
	for vi, v := range views {
		s := stats[v.suite]
		t.Logf("summary: %s: %d files, %d identical to %s; %d of %d %s assertions have the same result",
			v.suite, len(results[vi].units), s[0], v.interp, s[1], s[2], v.interp)
	}
	return results[0], results[1]
}

// --- LuaJIT-tests -----------------------------------------------------------

var reLJ = regexp.MustCompile(`^(TEST|LOAD|FILE|SKIP)\t`)

// ljResult is the outcome of the tests of a file: test number -> name and
// status ("ok", "skip", "fail: <message>"), or a load error.
type ljResult struct {
	load  string
	name  map[int]string
	stat  map[int]string
	count int
}

func parseLJ(lines []string) *ljResult {
	r := &ljResult{name: map[int]string{}, stat: map[int]string{}}
	for _, l := range lines {
		f := strings.SplitN(l, "\t", 5)
		switch {
		case f[0] == "LOAD" && len(f) >= 3:
			r.load = normalize(f[2])
		case f[0] == "TEST" && len(f) >= 4:
			n, _ := strconv.Atoi(f[1])
			r.name[n] = f[2]
			r.stat[n] = f[3]
			if len(f) == 5 {
				r.stat[n] += ": " + normalize(f[4])
			}
			r.count = max(r.count, n)
		}
	}
	return r
}

func luajitTests(t *testing.T, w *workspace, skips map[string]map[string]string) suiteResult {
	const suite = "luajit-tests"
	dir := filepath.Join(w.dir, "luajit-tests", "test", "LuaJIT-tests")
	driver := testdataPath(t, "lua/ljrun.lua")
	refs := map[string]map[string]*refRun{}
	for _, interp := range []string{refLua51, refLuaJIT} {
		refs[interp] = loadRefs(t, interp, suite)
	}
	res := suiteResult{suite: suite}
	// the files, as the index files select them (the reference list; ours
	// must be the same)
	list := refOf(t, refs[refLua51], refLua51, "--list")
	ourList := w.runDialect(lua.DialectLuaJIT, dir, timeout, "./?.lua;;", driver, "--list")
	res.units = append(res.units, "--list")
	if got := keepLines(ourList.stdout, reLJ); !slices.Equal(got, list.lines) {
		res.rows = append(res.rows, row{suite, "--list", "-", "differs from lua5.1's: " + normalize(strings.Join(got, " ")+" "+ourList.ended())})
	}
	var files []string
	for _, l := range list.lines {
		f := strings.Split(l, "\t")
		switch {
		case f[0] == "SKIP" && len(f) == 3:
			res.units = append(res.units, f[1])
			res.rows = append(res.rows, row{suite, f[1], "-", "skip: index tags " + f[2]})
		case f[0] == "FILE" && skips[suite][f[1]] != "":
			res.units = append(res.units, f[1])
			res.rows = append(res.rows, row{suite, f[1], "-", "skip: " + skips[suite][f[1]]})
		case f[0] == "FILE":
			files = append(files, f[1])
		}
	}
	var mu sync.Mutex
	var nTests, nOK, nLua51, nJIT, nOKvsJIT, nOKvsLua51 int
	forEach(files, func(f string) {
		r := w.runDialect(lua.DialectLuaJIT, dir, timeout, "./?.lua;;", driver, f)
		ours := parseLJ(keepLines(r.stdout, reLJ))
		var rr [2]*ljResult
		for i, interp := range []string{refLua51, refLuaJIT} {
			rr[i] = parseLJ(refOf(t, refs[interp], interp, f).lines)
		}
		var rows []row
		refs := func(get func(*ljResult) string) string {
			return fmt.Sprintf("[lua5.1: %s, luajit: %s]", get(rr[0]), get(rr[1]))
		}
		loadOf := func(x *ljResult) string {
			if x.load != "" {
				return "load fail"
			}
			return "ok"
		}
		if ours.load != "" {
			rows = append(rows, row{suite, f, "-", "load fail: " + ours.load + " " + refs(loadOf)})
		} else if r.exit != 0 || r.timedOut || ours.count < max(rr[0].count, rr[1].count) {
			rows = append(rows, row{suite, f, "-", fmt.Sprintf("%d tests, %s", ours.count, normalize(r.ended()))})
		}
		n := max(ours.count, rr[0].count, rr[1].count)
		mu.Lock()
		defer mu.Unlock()
		for i := 1; i <= n; i++ {
			st := ours.stat[i]
			name := firstNonEmpty(ours.name[i], rr[1].name[i], rr[0].name[i])
			statOf := func(x *ljResult) string { return firstNonEmpty(refStatus(x.stat[i]), "missing") }
			if st != "skip" {
				nTests++
				if st == "ok" {
					nOK++
				}
				if rr[0].stat[i] == "ok" {
					nLua51++
					if st == "ok" {
						nOKvsLua51++
					}
				}
				if rr[1].stat[i] == "ok" {
					nJIT++
					if st == "ok" {
						nOKvsJIT++
					}
				}
			}
			if st == "ok" || st == "skip" || ours.load != "" {
				continue
			}
			if st == "" {
				st = "missing"
			}
			rows = append(rows, row{suite, f, fmt.Sprintf("#%d %s", i, normalize(name)), st + " " + refs(statOf)})
		}
		res.units = append(res.units, f)
		res.rows = append(res.rows, rows...)
	})
	t.Logf("summary: luajit-tests: %d files run, %d skipped; %d tests: ok ours %d, lua5.1 %d, luajit %d; ours ok of lua5.1's ok %d/%d, of luajit's ok %d/%d",
		len(files), len(res.units)-len(files)-1, nTests, nOK, nLua51, nJIT, nOKvsLua51, nLua51, nOKvsJIT, nJIT)
	return res
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
