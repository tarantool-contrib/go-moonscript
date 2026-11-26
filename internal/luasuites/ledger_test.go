//go:build linux

package luasuites

import (
	"cmp"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The ledger, testdata/expected.tsv, holds the expected outcomes: one row
// per unit (a test file, or an archive for the parse check) or item (an
// assertion or a test) that is worth pinning, as
//
//	<suite> TAB <unit> TAB <item> TAB <outcome>
//
// (item "-" for the unit itself). A test fails when a row would be added,
// removed or changed; LUASUITES_UPDATE=1 (or -update) rewrites the rows of
// the units that ran instead.
const ledgerPath = "testdata/expected.tsv"

var update = flag.Bool("update", false, "rewrite testdata/expected.tsv (also LUASUITES_UPDATE=1)")

func updating() bool { return *update || os.Getenv("LUASUITES_UPDATE") != "" }

const updateHint = "LUASUITES_UPDATE=1 go test ./internal/luasuites"

type row struct {
	suite, unit, item, outcome string
}

func (r row) key() string { return r.suite + "\t" + r.unit + "\t" + r.item }

// suiteOrder is the order of the suites in the ledger.
var suiteOrder = []string{"lua5.1-tests", "harness", "harness-jit", "luajit-tests", "parse"}

// itemNumber is the number an item starts with ("#12 name"), for sorting.
func itemNumber(item string) int {
	s := strings.TrimPrefix(item, "#")
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil || s == item {
		return -1
	}
	return n
}

func compareRows(a, b row) int {
	return cmp.Or(
		cmp.Compare(slices.Index(suiteOrder, a.suite), slices.Index(suiteOrder, b.suite)),
		cmp.Compare(a.unit, b.unit),
		cmp.Compare(itemNumber(a.item), itemNumber(b.item)),
		cmp.Compare(a.item, b.item),
	)
}

// suiteResult is what a suite run gives: the units that ran and their rows.
type suiteResult struct {
	suite string
	units []string
	rows  []row
}

// unstable are the items whose outcome varies from run to run on our VM:
// their rows always say so, whatever the outcome. None at present
// (math.random's sequences are the reference's since its generator is C's).
var unstable = map[string]string{}

// stabilize replaces the rows of the unstable items of the units that ran.
func stabilize(res suiteResult) suiteResult {
	ran := map[string]bool{}
	for _, u := range res.units {
		ran[u] = true
	}
	rows := res.rows[:0:0]
	for _, r := range res.rows {
		if _, ok := unstable[r.key()]; !ok {
			rows = append(rows, r)
		}
	}
	for k, reason := range unstable {
		f := strings.SplitN(k, "\t", 3)
		if f[0] == res.suite && ran[f[1]] {
			rows = append(rows, row{f[0], f[1], f[2], "unstable (not compared): " + reason})
		}
	}
	res.rows = rows
	return res
}

var (
	resultsMu sync.Mutex
	results   []suiteResult
)

func readLedger(t *testing.T) []row {
	t.Helper()
	data, err := os.ReadFile(ledgerPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var rows []row
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.SplitN(line, "\t", 4)
		if len(f) != 4 {
			t.Fatalf("%s: bad row %q", ledgerPath, line)
		}
		rows = append(rows, row{f[0], f[1], f[2], f[3]})
	}
	return rows
}

// checkLedger compares the rows of a suite run with the ledger's, for the
// units that ran, or records them for the update.
func checkLedger(t *testing.T, res suiteResult) {
	t.Helper()
	res = stabilize(res)
	if updating() {
		resultsMu.Lock()
		results = append(results, res)
		resultsMu.Unlock()
		return
	}
	ran := map[string]bool{}
	for _, u := range res.units {
		ran[u] = true
	}
	want := map[string]row{}
	for _, r := range readLedger(t) {
		if r.suite == res.suite && ran[r.unit] {
			want[r.key()] = r
		}
	}
	got := map[string]row{}
	for _, r := range res.rows {
		got[r.key()] = r
	}
	var msgs []string
	for k, g := range got {
		w, ok := want[k]
		switch {
		case !ok && g.outcome == "pass":
			msgs = append(msgs, fmt.Sprintf("%s %s: new unit passes; update the ledger", g.unit, g.item))
		case !ok:
			msgs = append(msgs, fmt.Sprintf("%s %s: newly fails: %s", g.unit, g.item, g.outcome))
		case w.outcome == g.outcome:
		case g.outcome == "pass":
			msgs = append(msgs, fmt.Sprintf("%s %s: newly passes (was: %s); update the ledger", g.unit, g.item, w.outcome))
		case w.outcome == "pass":
			msgs = append(msgs, fmt.Sprintf("%s %s: newly fails: %s", g.unit, g.item, g.outcome))
		default:
			msgs = append(msgs, fmt.Sprintf("%s %s: changed:\n\twas: %s\n\tnow: %s", g.unit, g.item, w.outcome, g.outcome))
		}
	}
	for k, w := range want {
		if _, ok := got[k]; !ok {
			msgs = append(msgs, fmt.Sprintf("%s %s: newly passes or matches the reference (was: %s); update the ledger", w.unit, w.item, w.outcome))
		}
	}
	slices.Sort(msgs)
	const maxMsgs = 60
	for i, m := range msgs {
		if i == maxMsgs {
			t.Errorf("... and %d more differences", len(msgs)-maxMsgs)
			break
		}
		t.Error(m)
	}
	if len(msgs) > 0 {
		t.Logf("%d differences from %s; if they are expected: %s", len(msgs), ledgerPath, updateHint)
	}
}

const ledgerHeader = `# Expected outcomes of the Lua test suites on our VM (see doc.go and
# testdata/README.md): <suite> TAB <unit> TAB <item> TAB <outcome>.
# Generated by: ` + updateHint + `
`

// writeLedger rewrites the ledger with the recorded results: the rows of
// the units that ran replace the old ones, the others stay.
func writeLedger(t *testing.T) {
	t.Helper()
	ran := map[string]bool{}
	var rows []row
	for _, res := range results {
		for _, u := range res.units {
			ran[res.suite+"\t"+u] = true
		}
		rows = append(rows, res.rows...)
	}
	if len(ran) == 0 {
		return
	}
	for _, r := range readLedger(t) {
		if !ran[r.suite+"\t"+r.unit] {
			rows = append(rows, r)
		}
	}
	slices.SortFunc(rows, compareRows)
	var b strings.Builder
	b.WriteString(ledgerHeader)
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r.suite, r.unit, r.item, r.outcome)
	}
	if err := os.WriteFile(filepath.FromSlash(ledgerPath), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: %d rows", ledgerPath, len(rows))
}
