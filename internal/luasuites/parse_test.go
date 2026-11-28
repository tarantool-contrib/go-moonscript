//go:build linux

package luasuites

import (
	"fmt"
	"strings"
	"testing"

	lua "github.com/tarantool-contrib/go-moonscript/lua"

	moonrt "github.com/tarantool-contrib/go-moonscript/runtime"
)

// isLuaFile tells the files of the archives the parse check compiles: Lua
// sources and lua-Harness-tests' .t scripts.
func isLuaFile(name string) bool {
	return strings.HasSuffix(name, ".lua") || strings.HasSuffix(name, ".t")
}

// chunkSource is what the parse check compiles: the file, with a first line
// starting with "#" emptied, as lua.c's loadfile skips it. The reference
// script, testdata/lua/parse.lua, does the same.
func chunkSource(src []byte) string {
	s := string(src)
	if strings.HasPrefix(s, "#") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			return s[i:]
		}
		return ""
	}
	return s
}

// parseOutcome is "ok" or "error <message>" for a parse result line
// ("<path>\tok" or "<path>\terror\t<message>"), keyed by path.
func parseOutcomes(lines []string) map[string]string {
	m := map[string]string{}
	for _, l := range lines {
		f := strings.SplitN(l, "\t", 3)
		switch {
		case len(f) == 2:
			m[f[0]] = f[1]
		case len(f) == 3:
			m[f[0]] = f[1] + " " + normalize(f[2])
		}
	}
	return m
}

// parseTests compiles every Lua file of the archives with loadstring, as
// testdata/lua/parse.lua does on lua5.1 and luajit: on a state of
// DialectLua51 for lua5.1's outcomes, of DialectLuaJIT (its Load) for
// luajit's. It gives a row for each file accepted or rejected unlike the
// reference, or rejected with another message.
func parseTests(t *testing.T, w *workspace) suiteResult {
	const suite = "parse"
	refs := map[string]map[string]*refRun{}
	for _, interp := range []string{refLua51, refLuaJIT} {
		refs[interp] = loadRefs(t, interp, suite)
	}
	L := moonrt.NewState()
	defer L.Close()
	loadstring := L.GetGlobal("loadstring")
	LJ := lua.NewState(lua.Options{Dialect: lua.DialectLuaJIT, SkipOpenLibs: true})
	defer LJ.Close()
	res := suiteResult{suite: suite}
	var files int
	var diff, rejected, msgSame [2]int
	for _, a := range archives {
		res.units = append(res.units, a)
		ref51 := parseOutcomes(refOf(t, refs[refLua51], refLua51, a).lines)
		refJIT := parseOutcomes(refOf(t, refs[refLuaJIT], refLuaJIT, a).lines)
		for _, f := range w.files[a] {
			if !isLuaFile(f.name) {
				continue
			}
			files++
			src := chunkSource(f.src)
			ours51 := "ok"
			if err := L.CallByParam(lua.P{Fn: loadstring, NRet: 2, Protect: true},
				lua.LString(src), lua.LString("=src")); err != nil {
				ours51 = "error " + normalize(err.Error())
			} else {
				if L.Get(-2) == lua.LNil {
					ours51 = "error " + normalize(lua.LVAsString(L.Get(-1)))
				}
				L.Pop(2)
			}
			oursJIT := "ok"
			if _, err := LJ.Load(strings.NewReader(src), "src"); err != nil {
				oursJIT = "error " + normalize(err.Error())
			}
			r51, rJIT := ref51[f.name], refJIT[f.name]
			if r51 == "" || rJIT == "" {
				t.Errorf("%s/%s: no reference (run scripts/gen-luasuites-ref.sh)", a, f.name)
			}
			acc := func(s string) string { s, _, _ = strings.Cut(s, " "); return s }
			var unlike []string
			for i, c := range []struct{ ours, ref, interp string }{{ours51, r51, refLua51}, {oursJIT, rJIT, refLuaJIT}} {
				if acc(c.ours) == "error" {
					rejected[i]++
					if c.ours == c.ref {
						msgSame[i]++
					}
				}
				if acc(c.ours) != acc(c.ref) {
					diff[i]++
				}
				if c.ours != c.ref {
					unlike = append(unlike, fmt.Sprintf("%s [%s: %s]", c.ours, c.interp, c.ref))
				}
			}
			if len(unlike) > 0 {
				res.rows = append(res.rows, row{suite, a, f.name, strings.Join(unlike, "; ")})
			}
		}
	}
	t.Logf("summary: parse: %d files; DialectLua51: accepted/rejected unlike lua5.1: %d, %d rejected, %d with lua5.1's message; "+
		"DialectLuaJIT: accepted/rejected unlike luajit: %d, %d rejected, %d with luajit's message",
		files, diff[0], rejected[0], msgSame[0], diff[1], rejected[1], msgSame[1])
	return res
}
