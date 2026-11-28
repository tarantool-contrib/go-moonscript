package flow_test

import (
	"fmt"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lua/flow"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

// checkSource analyzes a source in a dialect, with the default options and
// with luacheck's quirks and a no-return function, and checks the
// invariants (checkProgram). It returns false if the source doesn't parse.
func checkSource(t testing.TB, name string, src []byte, d scope.Dialect, maxPairs int) bool {
	t.Helper()
	chunk, err := parse.ParseChunk(src, name, parse.Options{Dialect: parseDialect(d)})
	if err != nil {
		return false
	}
	info, err := scope.Resolve(chunk, d)
	if err != nil {
		t.Errorf("%s (%v): resolve: %v", name, d, err)
		return true
	}
	luacheckOpts := flow.LuacheckOptions(true, true, true, true)
	for _, opts := range []*flow.Options{nil, luacheckOpts, {NoReturn: []string{"error", "os.exit"}}} {
		p := flow.Analyze(info, opts)
		pairs := maxPairs
		if opts == luacheckOpts {
			pairs = 0 // luacheck's ends of scopes are no reaching definitions
		}
		for _, e := range checkProgram(chunk, info, p, pairs) {
			t.Errorf("%s (%v, %v): %s", name, d, opts != nil, e)
		}
		// the queries don't panic
		for _, g := range p.Graphs {
			g.Returns()
			g.AtMostOnce()
		}
		p.UnusedRecursive()
		luacheckLint(chunk, info, p, false)
	}
	return true
}

// TestCorpus checks the invariants of the Programs of the corpus (the
// fork's testdata and the luasuites archives) and of random programs, in
// both dialects.
func TestCorpus(t *testing.T) {
	files := map[string][]byte{}
	if testing.Short() {
		for name, b := range luaFiles(t, "../testdata") {
			files[name] = b
		}
	} else {
		files = corpus(t)
	}
	n := 2000
	if testing.Short() {
		n = 200
	}
	for i := range n {
		files[fmt.Sprintf("gen/%04d.lua", i)] = []byte(genProgram(uint64(i), i%2 == 0))
	}
	parsed := 0
	for _, name := range sortedKeys(files) {
		for _, d := range dialects {
			if checkSource(t, name, files[name], d, 20000) {
				parsed++
			}
		}
	}
	t.Logf("%d sources, %d parses checked", len(files), parsed)
}

// FuzzAnalyze analyzes mutated sources: nothing panics and the invariants
// hold, with the reaching definitions checked against a walk.
func FuzzAnalyze(f *testing.F) {
	for i := range 64 {
		f.Add([]byte(genProgram(uint64(i), i%2 == 0)))
	}
	for _, src := range []string{
		"local x = 1 x = 2 return x",
		"while true do end print(1)",
		"for i = 1, 2 do return i end",
		"local f f = function() return f end",
		"::a:: local x = 1 goto a",
		"repeat local x until x",
		"if a then elseif b then else end",
		"local t = {} t.x = t.y",
	} {
		f.Add([]byte(src))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 1<<14 {
			return
		}
		for _, d := range dialects {
			checkSource(t, "fuzz", src, d, 5000)
		}
	})
}
