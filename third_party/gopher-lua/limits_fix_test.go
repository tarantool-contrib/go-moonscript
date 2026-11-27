package lua

// go-moonscript patch: register overflows of chunks at the limits, with
// the results of lua5.1 and luajit (PUC Lua 5.1.5 and LuaJIT 2.1 of the
// luajitref:2.1 image, loadstring(src, "=input")): one family of
// expressions or statements per entry, after 190 locals (in a vararg
// function for the ones with "..."), at the sizes 35 to 65. Each dialect
// gives its interpreter's error near the same token, on the same line, and
// in the same place relative to the syntax error after it: the parser's
// models of the code generators (parse/lua51regs.go, parse/jitcode.go)
// find the overflows.

import (
	"fmt"
	"strings"
	"testing"
)

type limitRun struct {
	from int
	want string
}

func joinN(s, sep string, n int) string { return strings.Repeat(s+sep, n-1) + s }

func globals260(number func(int) string) string {
	var b strings.Builder
	for i := 0; i < 260; i++ {
		fmt.Fprintf(&b, "y%d = %s\n", i, number(i))
	}
	return b.String()
}

func one(int) string             { return "1" }
func half(i int) string          { return fmt.Sprintf("%d.5", i) }
func rep(s string, n int) string { return strings.Repeat(s, n) }

func params(n int) string {
	var p []string
	for i := 0; i < min(n, 9); i++ {
		p = append(p, fmt.Sprintf("p%d", i))
	}
	return strings.Join(p, ",")
}

var limitFamilies = map[string]func(n int) string{
	"callk":      func(n int) string { return "f(" + joinN("1", ",", n) + ")" },
	"calls":      func(n int) string { return "f(" + joinN("'a'", ",", n) + ")" },
	"callv":      func(n int) string { return "f(" + joinN("v1", ",", n) + ")" },
	"callg":      func(n int) string { return "f(" + joinN("g", ",", n) + ")" },
	"callva":     func(n int) string { return "f(" + joinN("g", ",", n) + ", ...)" },
	"nest":       func(n int) string { return rep("f(", n) + rep(")", n) },
	"nest1":      func(n int) string { return rep("f(1, ", n) + "1" + rep(")", n) },
	"concat":     func(n int) string { return "x = " + joinN("v1", " .. ", n) },
	"concatg":    func(n int) string { return "x = " + joinN("g", " .. ", n) },
	"concatk":    func(n int) string { return "x = " + joinN("'s'", " .. ", n) },
	"add":        func(n int) string { return "x = " + joinN("g", " + (", n) + rep(")", n-1) },
	"addk":       func(n int) string { return "x = " + joinN("1", " + (", n) + rep(")", n-1) },
	"addl":       func(n int) string { return "x = " + joinN("v1", " + (", n) + rep(")", n-1) },
	"pow":        func(n int) string { return "x = " + joinN("g", " ^ ", n) },
	"powk":       func(n int) string { return "x = " + joinN("2", " ^ ", n) },
	"cmp":        func(n int) string { return "x = " + joinN("g", " == (", n) + rep(")", n-1) },
	"lt":         func(n int) string { return "x = " + joinN("g", " < (", n) + rep(")", n-1) },
	"and":        func(n int) string { return "x = " + joinN("g", " and (", n) + rep(")", n-1) },
	"or":         func(n int) string { return "x = " + joinN("g", " or (", n) + rep(")", n-1) },
	"andcall":    func(n int) string { return "x = " + joinN("g", " and f(", n) + rep(")", n-1) },
	"not":        func(n int) string { return "x = f(" + joinN("not g", ", ", n) + ")" },
	"notnot":     func(n int) string { return "x = f(" + joinN("not not g", ", ", n) + ")" },
	"unm":        func(n int) string { return "x = f(" + joinN("-g", ", ", n) + ")" },
	"len":        func(n int) string { return "x = f(" + joinN("#g", ", ", n) + ")" },
	"unmk":       func(n int) string { return "x = f(" + joinN("- 1", ", ", n) + ")" },
	"table":      func(n int) string { return "x = {" + joinN("g", ",", n) + "}" },
	"tablenest":  func(n int) string { return "x = " + rep("{", n) + rep("}", n) },
	"tablerec":   func(n int) string { return "x = {" + joinN("a = {", ",", n) + rep("}", n) },
	"tableidx":   func(n int) string { return "x = {" + joinN("[g] = {", ",", n) + rep("}", n) },
	"tablek":     func(n int) string { return "x = {" + joinN("[1] = f(", ",", n) + rep(")", n-1) + ")}" },
	"index":      func(n int) string { return "x = " + joinN("t", "g[", n) + rep("]", n-1) },
	"indexk":     func(n int) string { return "x = t" + rep("[1]", n) },
	"dot":        func(n int) string { return "x = " + rep("(", n) + "g" + rep(").a", n) },
	"method":     func(n int) string { return "x = " + rep("o:m(", n) + rep(")", n) },
	"method1":    func(n int) string { return "x = " + rep("o:m(1, ", n) + "1" + rep(")", n) },
	"ret":        func(n int) string { return "return " + joinN("g", ", ", n) },
	"retl":       func(n int) string { return "return " + joinN("v1", ", ", n) },
	"retva":      func(n int) string { return "return " + joinN("g", ", ", n) + ", ..." },
	"assign":     func(n int) string { return joinN("a", ", ", n) + " = " + joinN("g", ", ", n) },
	"assignk":    func(n int) string { return joinN("a", ", ", n) + " = " + joinN("1", ", ", n) },
	"assignidx":  func(n int) string { return joinN("t.a", ", ", n) + " = " + joinN("g", ", ", n) },
	"assignidxl": func(n int) string { return joinN("v2[v3]", ", ", n) + ", v2 = " + joinN("g", ", ", n+1) },
	"assignmany": func(n int) string { return "a, b = " + joinN("g", ", ", n) },
	"assignfew":  func(n int) string { return joinN("a", ", ", n) + " = f()" },
	"local":      func(n int) string { return "local " + joinN("a", ", ", min(n, 9)) + " = " + joinN("g", ", ", n) },
	"localfew": func(n int) string {
		return "do local " + joinN("a", ", ", min(n, 9)) + " end f(" + joinN("1", ",", n) + ")"
	},
	"for":   func(n int) string { return "for i = f(" + joinN("1", ",", n) + "), 2 do end" },
	"forin": func(n int) string { return "for a, b in f(" + joinN("1", ",", n) + ") do end" },
	"forinx": func(n int) string {
		return "for " + joinN("a", ", ", min(n, 9)) + " in f(" + joinN("1", ",", n) + ") do end"
	},
	"if":     func(n int) string { return "if f(" + joinN("1", ",", n) + ") then end" },
	"while":  func(n int) string { return "while " + joinN("g", " and ", n) + " do end" },
	"repeat": func(n int) string { return "repeat until f(" + joinN("g", ",", n) + ")" },
	"func": func(n int) string {
		return "function g.a.b(" + params(n) + ") f(" + joinN("1", ",", n) + ") end"
	},
	"varg":     func(n int) string { return "x = {" + joinN("...", ",", n) + "}" },
	"vargcall": func(n int) string { return "f(" + joinN("(...)", ",", n) + ")" },
	"vargloc":  func(n int) string { return "local a, b, c = " + joinN("g", ",", n) + ", ..." },
	"nil":      func(n int) string { return "f(" + joinN("nil", ",", n) + ")" },
	"nilcmp":   func(n int) string { return "x = " + joinN("nil", " == (", n) + rep(")", n-1) },
	"true":     func(n int) string { return "x = " + joinN("true", " + (", n) + rep(")", n-1) },
	"str":      func(n int) string { return "x = " + joinN("'x'", " + (", n) + rep(")", n-1) },
	"sk": func(n int) string {
		return globals260(one) + "x = " + joinN("'x'", " + (", n) + rep(")", n-1)
	},
	"nk": func(n int) string {
		return globals260(half) + "x = " + joinN("1.5", " + (", n) + rep(")", n-1)
	},
	"nk2": func(n int) string {
		return globals260(half) + "t.x = " + joinN("7", " .. (", n) + rep(")", n-1)
	},
	"bk": func(n int) string {
		return globals260(half) + "x = " + joinN("true", " == (", n) + rep(")", n-1)
	},
	"setk": func(n int) string {
		return globals260(half) + joinN("t[1]", ", ", n) + " = " + joinN("2", ", ", n)
	},
	"closure":   func(n int) string { return "f(" + joinN("function() end", ",", n) + ")" },
	"paren":     func(n int) string { return "f(" + joinN("(g)", ",", n) + ")" },
	"parencall": func(n int) string { return "f(" + joinN("(f())", ",", n) + ")" },
	"lines":     func(n int) string { return "f(\n" + joinN("1", ",\n", n) + "\n)" },
	"linesc":    func(n int) string { return "x = \n" + joinN("g", "\n.. ", n) + "\n" },
}

func limitSource(family string, n int) string {
	var pre strings.Builder
	for i := 0; i < 190; i++ {
		fmt.Fprintf(&pre, "local v%d = 1\n", i)
	}
	body := limitFamilies[family](n)
	if strings.Contains(body, "...") {
		return "local function vf(...)\n" + pre.String() + body + "\nend\n"
	}
	return pre.String() + body + "\n"
}

// loadResult is "ok" or "error <message>" for loadstring(src, "input").
func loadResult(t *testing.T, L *LState, src string) string {
	t.Helper()
	L.SetGlobal("src", LString(src))
	if err := L.DoString(`local f, e = loadstring(src, "input") res = f and "ok" or "error " .. e`); err != nil {
		t.Fatal(err)
	}
	return L.GetGlobal("res").String()
}

func TestRegisterLimits(t *testing.T) {
	for _, d := range []Dialect{DialectLua51, DialectLuaJIT} {
		L := NewState(Options{Dialect: d})
		for family, res := range limitResults {
			runs := res.lua51
			if d == DialectLuaJIT {
				runs = res.luajit
			}
			for n := 35; n <= 65; n++ {
				want := ""
				for _, r := range runs {
					if r.from <= n {
						want = r.want
					}
				}
				if got := loadResult(t, L, limitSource(family, n)); got != want {
					t.Errorf("%v %s-%d: got %q, want %q", d, family, n, got, want)
				}
			}
		}
		L.Close()
	}
}

var limitResults = map[string]struct{ lua51, luajit []limitRun }{
	"callk": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '<eof>'"}, {60, "error input:191: function or expression too complex near '1'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:192: function or expression too complex near '<eof>'"}, {59, "error input:191: function or expression too complex near '1'"}},
	},
	"calls": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '<eof>'"}, {60, "error input:191: function or expression too complex near ''a''"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:192: function or expression too complex near '<eof>'"}, {59, "error input:191: function or expression too complex near ''a''"}},
	},
	"callv": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '<eof>'"}, {60, "error input:191: function or expression too complex near 'v1'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:192: function or expression too complex near '<eof>'"}, {59, "error input:191: function or expression too complex near 'v1'"}},
	},
	"callg": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '<eof>'"}, {60, "error input:191: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:192: function or expression too complex near '<eof>'"}, {59, "error input:191: function or expression too complex near 'g'"}},
	},
	"callva": {
		lua51:  []limitRun{{35, "ok"}, {57, "error input:192: function or expression too complex near ')'"}, {58, "error input:192: function or expression too complex near '...'"}, {59, "error input:192: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {57, "error input:192: function or expression too complex near '...'"}, {59, "error input:192: function or expression too complex near 'g'"}},
	},
	"nest": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near '('"}},
		luajit: []limitRun{{35, "error input:191: function or expression too complex near '('"}},
	},
	"nest1": {
		lua51:  []limitRun{{35, "error input:191: function or expression too complex near 'f'"}},
		luajit: []limitRun{{35, "error input:191: function or expression too complex near 'f'"}},
	},
	"concat": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'v1'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'v1'"}},
	},
	"concatg": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
	},
	"concatk": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near ''s''"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near ''s''"}},
	},
	"add": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near ')'"}, {61, "error input:191: function or expression too complex near '('"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near ')'"}, {61, "error input:191: function or expression too complex near '('"}},
	},
	"addk": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"addl": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"pow": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
	},
	"powk": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"cmp": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near ')'"}, {61, "error input:191: function or expression too complex near '('"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near ')'"}, {61, "error input:191: function or expression too complex near '('"}},
	},
	"lt": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near ')'"}, {61, "error input:191: function or expression too complex near '('"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near ')'"}, {61, "error input:191: function or expression too complex near '('"}},
	},
	"and": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"or": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"andcall": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near ')'"}, {61, "error input:191: function or expression too complex near 'f'"}},
		luajit: []limitRun{{35, "error input:191: function or expression too complex near '('"}},
	},
	"not": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:191: function or expression too complex near ')'"}, {60, "error input:191: function or expression too complex near ','"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:191: function or expression too complex near ')'"}, {59, "error input:191: function or expression too complex near ','"}},
	},
	"notnot": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:191: function or expression too complex near ')'"}, {60, "error input:191: function or expression too complex near ','"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:191: function or expression too complex near ')'"}, {59, "error input:191: function or expression too complex near ','"}},
	},
	"unm": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:191: function or expression too complex near ')'"}, {60, "error input:191: function or expression too complex near ','"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:191: function or expression too complex near ')'"}, {59, "error input:191: function or expression too complex near ','"}},
	},
	"len": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:191: function or expression too complex near ')'"}, {60, "error input:191: function or expression too complex near ','"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:191: function or expression too complex near ')'"}, {59, "error input:191: function or expression too complex near ','"}},
	},
	"unmk": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '<eof>'"}, {60, "error input:191: function or expression too complex near '-'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:192: function or expression too complex near '<eof>'"}, {59, "error input:191: function or expression too complex near '-'"}},
	},
	"table": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"tablenest": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near '{'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near '{'"}},
	},
	"tablerec": {
		lua51:  []limitRun{{35, "error input:191: unexpected symbol near ','"}},
		luajit: []limitRun{{35, "error input:191: unexpected symbol near ','"}},
	},
	"tableidx": {
		lua51:  []limitRun{{35, "error input:191: unexpected symbol near ','"}},
		luajit: []limitRun{{35, "error input:191: unexpected symbol near ','"}},
	},
	"tablek": {
		lua51:  []limitRun{{35, "error input:191: unexpected symbol near ','"}},
		luajit: []limitRun{{35, "error input:191: unexpected symbol near ','"}},
	},
	"index": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near ']'"}, {61, "error input:191: function or expression too complex near '['"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near ']'"}, {61, "error input:191: function or expression too complex near '['"}},
	},
	"indexk": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"dot": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"method": {
		lua51:  []limitRun{{35, "error input:191: function or expression too complex near '('"}},
		luajit: []limitRun{{35, "error input:191: function or expression too complex near '('"}},
	},
	"method1": {
		lua51:  []limitRun{{35, "error input:191: function or expression too complex near 'o'"}},
		luajit: []limitRun{{35, "error input:191: function or expression too complex near 'o'"}},
	},
	"ret": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
	},
	"retl": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'v1'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'v1'"}},
	},
	"retva": {
		lua51:  []limitRun{{35, "ok"}, {58, "error input:193: function or expression too complex near 'end'"}, {59, "error input:192: function or expression too complex near '...'"}, {60, "error input:192: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '...'"}, {61, "error input:192: function or expression too complex near 'g'"}},
	},
	"assign": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
	},
	"assignk": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near '1'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near '1'"}},
	},
	"assignidx": {
		lua51:  []limitRun{{35, "error input:191: function or expression too complex near 'g'"}, {60, "error input:191: function or expression too complex near '.'"}},
		luajit: []limitRun{{35, "error input:191: function or expression too complex near 'g'"}, {60, "error input:191: function or expression too complex near '.'"}},
	},
	"assignidxl": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:191: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {59, "error input:191: function or expression too complex near 'g'"}},
	},
	"assignmany": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
	},
	"assignfew": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}},
	},
	"local": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:192: function or expression too complex near '<eof>'"}, {61, "error input:191: function or expression too complex near 'g'"}},
	},
	"localfew": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '<eof>'"}, {60, "error input:191: function or expression too complex near '1'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:192: function or expression too complex near '<eof>'"}, {59, "error input:191: function or expression too complex near '1'"}},
	},
	"for": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:191: function or expression too complex near ','"}, {60, "error input:191: function or expression too complex near '1'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:191: function or expression too complex near ','"}, {59, "error input:191: function or expression too complex near '1'"}},
	},
	"forin": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:191: function or expression too complex near 'do'"}, {60, "error input:191: function or expression too complex near '1'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:191: function or expression too complex near 'do'"}, {59, "error input:191: function or expression too complex near '1'"}},
	},
	"forinx": {
		lua51:  []limitRun{{35, "error input:191: main function has more than 200 local variables"}},
		luajit: []limitRun{{35, "error input:191: main function has more than 200 local variables"}},
	},
	"if": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:191: function or expression too complex near 'then'"}, {60, "error input:191: function or expression too complex near '1'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:191: function or expression too complex near 'then'"}, {59, "error input:191: function or expression too complex near '1'"}},
	},
	"while": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"repeat": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '<eof>'"}, {60, "error input:191: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:192: function or expression too complex near '<eof>'"}, {59, "error input:191: function or expression too complex near 'g'"}},
	},
	"func": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"varg": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"vargcall": {
		lua51:  []limitRun{{35, "ok"}, {58, "error input:193: function or expression too complex near 'end'"}, {59, "error input:192: function or expression too complex near '('"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:192: function or expression too complex near '...'"}},
	},
	"vargloc": {
		lua51:  []limitRun{{35, "ok"}, {58, "error input:193: function or expression too complex near 'end'"}, {59, "error input:192: function or expression too complex near '...'"}, {60, "error input:192: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '...'"}, {61, "error input:192: function or expression too complex near 'g'"}},
	},
	"nil": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '<eof>'"}, {60, "error input:191: function or expression too complex near 'nil'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:192: function or expression too complex near '<eof>'"}, {59, "error input:191: function or expression too complex near 'nil'"}},
	},
	"nilcmp": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"true": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near ')'"}, {61, "error input:191: function or expression too complex near '('"}},
	},
	"str": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:191: function or expression too complex near ')'"}, {61, "error input:191: function or expression too complex near '('"}},
	},
	"sk": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:451: function or expression too complex near ')'"}, {61, "error input:451: function or expression too complex near '('"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:451: function or expression too complex near ')'"}, {61, "error input:451: function or expression too complex near '('"}},
	},
	"nk": {
		lua51:  []limitRun{{35, "ok"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"nk2": {
		lua51:  []limitRun{{35, "ok"}, {58, "error input:451: function or expression too complex near ')'"}, {59, "error input:451: function or expression too complex near '('"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:451: function or expression too complex near ')'"}, {59, "error input:451: function or expression too complex near '('"}},
	},
	"bk": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:451: function or expression too complex near ')'"}, {61, "error input:451: function or expression too complex near '('"}},
		luajit: []limitRun{{35, "ok"}},
	},
	"setk": {
		lua51:  []limitRun{{35, "error input:451: function or expression too complex near ','"}},
		luajit: []limitRun{{35, "error input:451: function or expression too complex near '2'"}, {60, "error input:451: function or expression too complex near '['"}},
	},
	"closure": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '<eof>'"}, {60, "error input:191: function or expression too complex near 'function'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:192: function or expression too complex near '<eof>'"}, {59, "error input:191: function or expression too complex near 'function'"}},
	},
	"paren": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:192: function or expression too complex near '<eof>'"}, {60, "error input:191: function or expression too complex near '('"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:192: function or expression too complex near '<eof>'"}, {59, "error input:191: function or expression too complex near '('"}},
	},
	"parencall": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:191: function or expression too complex near '('"}},
		luajit: []limitRun{{35, "ok"}, {57, "error input:191: function or expression too complex near '('"}},
	},
	"lines": {
		lua51:  []limitRun{{35, "ok"}, {59, "error input:252: function or expression too complex near '<eof>'"}, {60, "error input:251: function or expression too complex near '1'"}},
		luajit: []limitRun{{35, "ok"}, {58, "error input:251: function or expression too complex near '<eof>'"}, {59, "error input:250: function or expression too complex near '1'"}},
	},
	"linesc": {
		lua51:  []limitRun{{35, "ok"}, {60, "error input:253: function or expression too complex near '<eof>'"}, {61, "error input:252: function or expression too complex near 'g'"}},
		luajit: []limitRun{{35, "ok"}, {60, "error input:253: function or expression too complex near '<eof>'"}, {61, "error input:252: function or expression too complex near 'g'"}},
	},
}
