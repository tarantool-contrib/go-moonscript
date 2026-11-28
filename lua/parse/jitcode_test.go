package parse

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The snippets at the limits of LuaJIT's variable stack and code generator
// and their results are go-luascope's (parser/snippets_test.go, the
// limitSnippets and codegenSnippets; results from its
// testdata/golden/snippets.golden.gz, recorded with LuaJIT 2.1;
// https://github.com/bigbes/go-luascope, BSD-2-Clause, Copyright (c) 2026,
// Eugene Blikh; see NOTICE).

type jitSnippet struct {
	name, src, want string
}

func jitLimitSnippets() []jitSnippet {
	// vars declares n locals in blocks: a local leaves its block but stays
	// in the variable stack until the end of the function
	vars := func(n int) string {
		var b strings.Builder
		for ; n > 0; n -= 100 {
			b.WriteString("do local a" + strings.Repeat(",a", min(n, 100)-1) + " end\n")
		}
		return b.String()
	}
	const over = "chunk has more than 65476 local variables"
	return []jitSnippet{
		{"vstack-locals-65476", vars(65476), "ok"},
		{"vstack-locals-65477", vars(65477), "655: " + over},
		{"vstack-label", vars(65475) + "do ::l:: end", "ok"},
		{"vstack-label-over", vars(65476) + "do ::l:: end", "656: " + over},
		{"vstack-goto", vars(65474) + "do goto l ::l:: end", "ok"},
		{"vstack-goto-over", vars(65475) + "do goto l ::l:: end", "656: " + over},
		{"vstack-break", vars(65474) + "while x do break end", "ok"},
		// the label of the break needs an entry at the end of the loop
		{"vstack-break-label", vars(65475) + "while x do break end", "656: " + over},
		{"vstack-break-repeat", vars(65475) + "repeat break until x", "656: " + over},
		{"vstack-for", vars(65472) + "for i = 1, 2 do end", "ok"},
		{"vstack-for-over", vars(65473) + "for i = 1, 2 do end", "656: " + over},
		{"vstack-for-in", vars(65471) + "for k, v in x do end", "ok"},
		{"vstack-for-in-over", vars(65472) + "for k, v in x do end", "656: " + over},
		{"vstack-params", vars(65474) + "local function f(a) end", "ok"},
		{"vstack-params-over", vars(65475) + "local function f(a) end", "656: " + over},
		{"vstack-self", vars(65475) + "function t:m() end", "ok"},
		{"vstack-self-over", vars(65476) + "function t:m() end", "656: " + over},
		// a function counts the entries of the functions enclosing it and
		// drops its own at its end
		{"vstack-function-enclosing", vars(65475) + "local function f() " + vars(1) + " end", "656: " + over},
		{"vstack-function-after", "local function f() " + vars(65475) + " end " + vars(65475), "ok"},
		{"vstack-function-nested", vars(65000) + "local function f() " + vars(475) + " end", "ok"},
		{"vstack-function-nested-over", vars(65000) + "local function f() " + vars(476) + " end", "655: " + over},
		// a repeat loop with a captured local leaves through a hidden break
		{"vstack-repeat-upval", vars(65472) + "repeat local x local function f() return x end until x", "ok"},
		{"vstack-repeat-upval-over", vars(65473) + "repeat local x local function f() return x end until x", "656: " + over},
		{"vstack-repeat", vars(65473) + "repeat local x until x", "ok"},
	}
}

func jitCodegenSnippets() []jitSnippet {
	calls := func(n int) string { return strings.Repeat("f() ", n) } // 2 instructions each
	args := func(n int) string { return "f(" + strings.Repeat("a,", n-1) + "a)" }
	statements := func(n int, format string) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, format, i)
		}
		return b.String()
	}
	var list []jitSnippet
	pair := func(name string, ok int, src func(n int) string, want string) {
		list = append(list,
			jitSnippet{fmt.Sprintf("%s-%d", name, ok), src(ok), "ok"},
			jitSnippet{fmt.Sprintf("%s-%d", name, ok+1), src(ok + 1), want})
	}
	const jump = "1: control structure too long near "
	pair("while", 16381, func(n int) string { return "while x do " + calls(n) + "end" }, jump+"'end'")
	pair("while-true", 16383, func(n int) string { return "while true do " + calls(n) + "end" }, jump+"'end'")
	pair("if", 16383, func(n int) string { return "if x then " + calls(n) + "end" }, jump+"'<eof>'")
	pair("if-else", 16383, func(n int) string { return "if x then " + calls(n) + "else f() end" }, jump+"'('")
	pair("for", 16383, func(n int) string { return "for i = 1, 2 do " + calls(n) + "end" }, jump+"'end'")
	pair("for-in", 16383, func(n int) string { return "for k in x do " + calls(n) + "end" }, jump+"'end'")
	pair("repeat", 16382, func(n int) string { return "repeat " + calls(n) + "until x" }, jump+"'<eof>'")
	pair("goto-back", 16383, func(n int) string { return "::a:: " + calls(n) + "goto a" }, jump+"'<eof>'")
	pair("goto-forward", 16383, func(n int) string { return "goto a " + calls(n) + "::a::" }, jump+"'<eof>'")
	pair("break", 16379, func(n int) string {
		return "while x do if y then break end " + calls(n) + "end"
	}, jump+"'end'")
	pair("and", 10921, func(n int) string {
		return "x = y and f(" + strings.Repeat("g() + ", n) + "1)"
	}, jump+"'<eof>'")
	pair("args", 247, args, "1: function or expression too complex near '<eof>'")
	pair("locals-args", 46, func(n int) string {
		return "local " + strings.Repeat("a,", 199) + "a " + args(n+1)
	}, "1: function or expression too complex near '<eof>'")
	pair("return-fixup", 16382, func(n int) string {
		return "if x then return end " + calls(n) + " local function g() end"
	}, "1: function too long for return fixup near '<eof>'")
	pair("numbers", 65536, func(n int) string { return statements(n, "x=%d.5 ") },
		"1: main function has more than 65536 constants")
	pair("strings", 65535, func(n int) string { return statements(n, "x='%d' ") },
		"1: main function has more than 65536 constants")
	pair("functions", 65535, func(n int) string {
		return "local function f() " + strings.Repeat("x=function() end ", n) + "end"
	}, "1: function at line 1 has more than 65536 constants")
	return list
}

func parseResult(src string, d Dialect) string {
	_, err := ParseDialect(strings.NewReader(src), "=input", d)
	if err == nil {
		return "ok"
	}
	var pe *Error
	if !errors.As(err, &pe) {
		return "not a parse error: " + err.Error()
	}
	if pe.Token == "" {
		return fmt.Sprintf("%d: %s", pe.Pos.Line, pe.Message)
	}
	return fmt.Sprintf("%d: %s near '%s'", pe.Pos.Line, pe.Message, pe.Token)
}

// TestJITLimits checks LuaJIT's limits of the variable stack and of the
// code generator, which only DialectLuaJIT has.
func TestJITLimits(t *testing.T) {
	for _, c := range append(jitLimitSnippets(), jitCodegenSnippets()...) {
		if got := parseResult(c.src, LuaJIT); got != c.want {
			t.Errorf("%s: LuaJIT: got %q, want %q", c.name, got, c.want)
		}
		if strings.Contains(c.src, "goto") || strings.Contains(c.src, "::") {
			continue // not Lua 5.1
		}
		// Lua 5.1 has none of them, but the variable stack's snippets
		// declare more than its 32767 local variable records
		want := "ok"
		if strings.HasPrefix(c.name, "vstack-") {
			want = "0: too many local variables"
		}
		if got := parseResult(c.src, Lua51); got != want {
			t.Errorf("%s: Lua51: got %q, want %q", c.name, got, want)
		}
	}
}
