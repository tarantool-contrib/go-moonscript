package parse

// go-moonscript patch: tests of Options.Lua52Compat, LuaJIT's LJ_52
// syntax. The errors are those of LuaJIT 2.1 built with
// LUAJIT_ENABLE_LUA52COMPAT (and without it) on the same sources; on the
// 10,811 sources of the parity corpus, 1,543 of which LJ_52 reads
// differently, the option gives the outcome and message of such a build.

import (
	"reflect"
	"testing"
)

func TestLua52Compat(t *testing.T) {
	tests := []struct {
		src string
		// the errors without and with the option, "" for none
		plain, lj52 string
	}{
		{";", `=x:1: unexpected symbol near ';'`, ""},
		{"local a = 1;; ;", `=x:1: unexpected symbol near ';'`, ""},
		{"do ; end if x then ; else ; end repeat ; until x", `=x:1: unexpected symbol near ';'`, ""},
		{"function f() ; end", `=x:1: unexpected symbol near ';'`, ""},
		{"local t = {1; 2;}", "", ""},
		{"return;", "", ""},
		{"return; ;", `=x:1: '<eof>' expected near ';'`, `=x:1: '<eof>' expected near ';'`},
		{"while x do break f() end", `=x:1: 'end' expected near 'f'`, ""},
		{"while x do break; break end", `=x:1: 'end' expected near 'break'`, ""},
		{"break", `=x:1: no loop to break`, `=x:1: no loop to break`},
		{"f()\n(g)()", `=x:2: ambiguous syntax (function call x new statement) near '('`, ""},
		{"local a = b\n(f)()", `=x:2: ambiguous syntax (function call x new statement) near '('`, ""},
		{"f\n'x'", "", ""},
		{"goto l ::l::", "", ""},
		{"local goto = 1", "", `=x:1: '<name>' expected near 'goto'`},
		{"goto = 1", "", `=x:1: '<name>' expected near '='`},
		{"x = goto", "", `=x:1: unexpected symbol near 'goto'`},
		{"t = {goto = 1} t.goto = t:goto()", "", `=x:1: unexpected symbol near 'goto'`},
		{"function f(goto) end", "", `=x:1: <name> or '...' expected near 'goto'`},
		{"goto end", `=x:1: '=' expected near 'end'`, `=x:1: '<name>' expected near 'end'`},
		{"goto", `=x:1: '=' expected near '<eof>'`, `=x:1: '<name>' expected near '<eof>'`},
		{"::goto::", "", `=x:1: '<name>' expected near 'goto'`},
		{"for goto = 1, 2 do end", "", `=x:1: '<name>' expected near 'goto'`},
		{"local function goto() end", "", `=x:1: '<name>' expected near 'goto'`},
		{"a.b\n(c)", `=x:2: ambiguous syntax (function call x new statement) near '('`, ""},
		{"f{}\n(g)", `=x:2: ambiguous syntax (function call x new statement) near '('`, ""},
		{"x = (a)\n(b)", `=x:2: ambiguous syntax (function call x new statement) near '('`, ""},
		// Under LJ_52 the ";" after a label goes with it: the label is still
		// the last statement of its block, out of the scope of its locals.
		{"do goto l local x ::l:: ; end", `=x:1: <goto l> jumps into the scope of local 'x'`, ""},
		{"do goto l local x ::l:: ; f() end", `=x:1: <goto l> jumps into the scope of local 'x'`,
			`=x:1: <goto l> jumps into the scope of local 'x'`},
		{"goto l ::l:: ; ::m:: ;", "", ""},
	}
	for _, tt := range tests {
		for _, lj52 := range []bool{false, true} {
			want := tt.plain
			if lj52 {
				want = tt.lj52
			}
			_, err := ParseChunk([]byte(tt.src), "=x", Options{Dialect: LuaJIT, Lua52Compat: lj52})
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != want {
				t.Errorf("%q, Lua52Compat %v: got %q, want %q", tt.src, lj52, got, want)
			}
		}
		// Lua 5.1 is the same with the option.
		_, err51 := ParseChunk([]byte(tt.src), "=x", Options{Dialect: Lua51})
		_, err51c := ParseChunk([]byte(tt.src), "=x", Options{Dialect: Lua51, Lua52Compat: true})
		if !reflect.DeepEqual(err51, err51c) {
			t.Errorf("%q under Lua51: %v with the option, %v without", tt.src, err51c, err51)
		}
	}
}

// TestLua52CompatSameTree: a source that parses with and without the option
// has the same tree, spans included.
func TestLua52CompatSameTree(t *testing.T) {
	for _, src := range []string{
		"local a = require('a'); local t = {1; 2}\nwhile a do f(a) break end\n",
		"goto l\n::l:: local x <const> = 1",
		"for i = 1, 2 do if i then break end end return f\n'x'",
	} {
		plain, err1 := ParseChunk([]byte(src), "=x", Options{Dialect: LuaJIT})
		lj52, err2 := ParseChunk([]byte(src), "=x", Options{Dialect: LuaJIT, Lua52Compat: true})
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("%q: %v, %v", src, err1, err2)
		}
		if err1 == nil && Dump(plain.Stmts) != Dump(lj52.Stmts) {
			t.Errorf("%q: the trees differ:\n%s\n%s", src, Dump(plain.Stmts), Dump(lj52.Stmts))
		}
		if err1 == nil && !reflect.DeepEqual(plain.Stmts, lj52.Stmts) {
			t.Errorf("%q: the trees differ in their spans or lines", src)
		}
	}
}
