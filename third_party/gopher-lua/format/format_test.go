package format_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/format"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
)

// hazardSources are sources whose formatting must avoid an ambiguity or
// keep a form: they are also seeds of the fuzz targets.
var hazardSources = []string{
	"local x = y;\n(f or g)(1)\n",
	"x = - -y\nz = -(-1)\n",
	"x = a .. .5 .. 1 .. 2\n",
	"x = t[ [[s]] ] .. t[ [=[s]=] ]\n",
	"x = {[ [[k]] ] = 1}\n",
	"f --[[c]] (g)\n",
	"local a = (f())\nreturn (...)\n",
	"x = (a).b + ((c))\n",
	"x = a.b + a['b'] + a[\"b\"]\n",
	"require'x' f{1} g\"s\" o:m{} o:m'a'\n",
	"x = 0x10 + 1e3 + .5 + 3. + 0XaP1\n",
	"x = '\\'' .. \"\\\"\" .. '\\65\\066' .. [[\nlong]]\n",
	"if a then else if b then end end\n",
	"local t = {\n1, 2}\n",
	"return\n",
	"#!/usr/bin/lua\nprint(1)\n",
	"for i = 1, 2 do local x = i end\n",
	"local s = \"a\\\n b\"\n",
}

func formatSource(t *testing.T, src string, opts format.Options) string {
	t.Helper()
	out, err := format.Source([]byte(src), opts)
	if err != nil {
		t.Fatalf("format %q: %v", src, err)
	}
	return string(out)
}

// TestFormat checks the formatted text of sources.
func TestFormat(t *testing.T) {
	cases := []struct {
		src, want string
		opts      format.Options
	}{
		// spacing and indentation
		{src: "local  x=1+2*3", want: "local x = 1 + 2 * 3\n"},
		{src: "if a then b() end", want: "if a then b() end\n"},
		{src: "if a then\nb() end", want: "if a then\n    b()\nend\n"},
		{src: "if a then b() c() end", want: "if a then\n    b()\n    c()\nend\n"},
		{src: "if a then\nb()\nend", want: "if a then\n\tb()\nend\n", opts: format.Options{UseTabs: true}},
		{src: "if a then\nb()\nend", want: "if a then\n  b()\nend\n", opts: format.Options{IndentWidth: 2}},
		{src: "function f(a,b,...) return a end", want: "function f(a, b, ...) return a end\n"},
		{src: "function f(a)\nreturn a end", want: "function f(a)\n    return a\nend\n"},
		{src: "local function f() end", want: "local function f() end\n"},
		{src: "do end while x do end repeat until x", want: "do end\nwhile x do end\nrepeat until x\n"},
		{src: "for k,v in pairs(t) do end", want: "for k, v in pairs(t) do end\n"},
		// literals as written
		{src: "x = a['b'] + a.b + a[\"b\"]", want: "x = a['b'] + a.b + a[\"b\"]\n"},
		{src: "x = {['b'] = 1, b = 2}", want: "x = {['b'] = 1, b = 2}\n"},
		{src: "x = 0x10 + 1e3 + .5 + 3.", want: "x = 0x10 + 1e3 + .5 + 3.\n"},
		{src: "x = 'a' .. [==[b]==]", want: "x = 'a' .. [==[b]==]\n"},
		{src: "require'x' f{1} g\"s\" o:m{}", want: "require 'x'\nf{1}\ng \"s\"\no:m{}\n"},
		{src: "f('x') f({1})", want: "f('x')\nf({1})\n"},
		{src: "x = ((a)) + (b)", want: "x = ((a)) + (b)\n"},
		{src: "if a then elseif b then end", want: "if a then elseif b then end\n"},
		{src: "if a then\nelse if b then end end", want: "if a then\nelse\n    if b then end\nend\n"},
		// quotes
		{src: `x = 'a' .. "b" .. 'c"' .. '\'' .. [[d]]`, want: `x = "a" .. "b" .. 'c"' .. "'" .. [[d]]` + "\n",
			opts: format.Options{Quotes: format.QuotesDouble}},
		{src: `x = 'a' .. "b" .. "c'" .. "\""`, want: `x = 'a' .. 'b' .. "c'" .. '"'` + "\n",
			opts: format.Options{Quotes: format.QuotesSingle}},
		// blank lines: kept, collapsed to one, none at the start or end of
		// a block
		{src: "a()\n\n\n\nb()\nc()", want: "a()\n\nb()\nc()\n"},
		{src: "do\n\na()\n\nend", want: "do\n    a()\nend\n"},
		{src: "t = {\n1,\n\n2,\n}", want: "t = {\n    1,\n\n    2,\n}\n"},
		// semicolons
		{src: "a = 1; b = 2;", want: "a = 1\nb = 2\n"},
		{src: "a = 1; b = 2;", want: "a = 1;\nb = 2;\n", opts: format.Options{KeepSemicolons: true}},
		{src: "a = b;(f)()", want: "a = b;\n(f)()\n"},
		// tables
		{src: "t = {1, 2,}", want: "t = {1, 2}\n"},
		{src: "t = {\n1, 2}", want: "t = {\n    1,\n    2\n}\n"},
		{src: "t = {\n1, 2}", want: "t = {\n    1,\n    2,\n}\n", opts: format.Options{TrailingComma: true}},
		{src: "t = {\n1; 2;}", want: "t = {\n    1,\n    2,\n}\n"},
		{src: "t = {a = 1, b = {c = 2, d = 3}}", want: "t = {\n    a = 1,\n    b = {c = 2, d = 3}\n}\n", opts: format.Options{Width: 24}},
		// line breaking
		{src: "local x = aaaa + bbbb + cccc", want: "local x = aaaa +\n    bbbb +\n    cccc\n", opts: format.Options{Width: 20}},
		{src: "f(aaaa, bbbb, cccc)", want: "f(\n    aaaa,\n    bbbb,\n    cccc\n)\n", opts: format.Options{Width: 12}},
		{src: "f(a, function() return 1 end)", want: "f(a, function()\n    return 1\nend)\n", opts: format.Options{Width: 20}},
		{src: "f(a, {b = 1, c = 2})", want: "f(a, {\n    b = 1,\n    c = 2\n})\n", opts: format.Options{Width: 16}},
		{src: "if aaaa and bbbb then x() end", want: "if aaaa and\n        bbbb then\n    x()\nend\n", opts: format.Options{Width: 16}},
		// the header
		{src: "#!/usr/bin/lua\nprint(1)", want: "#!/usr/bin/lua\nprint(1)\n"},
		{src: "", want: ""},
	}
	for _, c := range cases {
		if got := formatSource(t, c.src, c.opts); got != c.want {
			t.Errorf("%q: got\n%s\nwant\n%s", c.src, got, c.want)
		}
	}
}

// TestFormatHazards formats the hazard sources and checks them.
func TestFormatHazards(t *testing.T) {
	for _, src := range hazardSources {
		for _, d := range dialects {
			var st checkStats
			f, checked := checkSource([]byte(src), format.Options{Dialect: d}, &st)
			if !checked && d == parse.LuaJIT {
				t.Errorf("%q does not compile", src)
			}
			if f != "" {
				t.Errorf("%q (%s): %s", src, dialectName(d), f)
			}
		}
	}
	got := formatSource(t, "f --[[c]] (g)", format.Options{})
	if got != "f( --[[c]] g)\n" && got != "f(--[[c]] g)\n" {
		t.Errorf("comment before the arguments: %q", got)
	}
	if got := formatSource(t, "x = t[ [[s]] ] .. t[ [[s]] .. 1]", format.Options{}); got != "x = t[ [[s]] ] .. t[ [[s]] .. 1 ]\n" {
		t.Errorf("long string key: %q", got)
	}
}

// commentSources are sources with comments in every kind of place: also
// seeds of the fuzz targets.
var commentSources = []string{
	"-- a\nlocal x = 1 -- b\n-- c\n",
	"local t = { -- a\n  1, -- b\n  -- c\n  2 --[[d]], [3] = --[[e]] 4,\n  -- f\n}\n",
	"f( -- a\n  x, -- b\n  y -- c\n)\n",
	"f(--[[a]])\nlocal t = {--[[b]]}\nlocal function g(--[[c]]) end\n",
	"if a then -- a\n  -- b\n  x() -- c\n  -- d\nelseif b then -- e\n  -- f\nelse -- g\nend -- h\n",
	"function f() -- a\nend\ndo -- b\nend\nwhile x do --[[c]] end\nrepeat -- d\nuntil x\n",
	"local --[[a]] x --[[b]] = --[[c]] 1\n",
	"x = a + --[[a]] b * -- b\n c\n",
	"x = (--[[a]] a --[[b]])\n",
	"--[==[\nlong\n]==] x = 1\n",
	"x = 1\n\n-- a\n\n-- b\ny = 2\n\n-- c\n",
	"return -- a\n",
	"-- only a comment",
	"local a, -- a\n b = 1, 2\n",
	"function a. --[[a]] b: --[[b]] c() end\n",
	"x = f --[[a]] 's' .. g --[[b]] {}\n",
	"x = a:b --[[a]] (1)\n",
	"for i --[[a]] = 1, 2 --[[b]] do end\nfor k --[[c]] in x do end\n",
	"if a\n-- a\nthen end\n",
	"a()\n-- a\n;(f)()\n",
	"goto --[[a]] l ::l:: --[[b]]\n",
}

// TestFormatComments checks the checks on the comment sources: every
// comment kept, the code the same, idempotence.
func TestFormatComments(t *testing.T) {
	for _, src := range commentSources {
		for _, d := range dialects {
			var st checkStats
			f, checked := checkSource([]byte(src), format.Options{Dialect: d, Width: 40}, &st)
			if !checked && d == parse.LuaJIT {
				t.Errorf("%q does not compile", src)
			}
			if f != "" {
				t.Errorf("%q (%s): %s", src, dialectName(d), f)
			}
		}
	}
	cases := []struct{ src, want string }{
		{"-- a\nlocal x = 1 -- b\n-- c\n", "-- a\nlocal x = 1 -- b\n-- c\n"},
		{"local t = { -- a\n  1, -- b\n  -- c\n  2 --[[d]],\n}\n", "local t = { -- a\n    1, -- b\n    -- c\n    2, --[[d]]\n}\n"},
		{"f( -- a\n  x, -- b\n  y -- c\n)\n", "f( -- a\n    x, -- b\n    y -- c\n)\n"},
		{"f(--[[a]])\nlocal t = {--[[b]]}\n", "f( --[[a]] )\nlocal t = { --[[b]] }\n"},
		{"if a then -- a\n  -- b\n  x() -- c\n  -- d\nelse -- g\nend -- h\n",
			"if a then -- a\n    -- b\n    x() -- c\n    -- d\nelse -- g\nend -- h\n"},
		{"function f() -- a\nend\ndo -- b\nend\n", "function f() -- a\nend\ndo -- b\nend\n"},
		{"local --[[a]] x --[[b]] = --[[c]] 1\n", "local --[[a]] x --[[b]] = --[[c]] 1\n"},
		{"x = 1\n\n-- a\n\n-- b\ny = 2\n\n-- c\n", "x = 1\n\n-- a\n\n-- b\ny = 2\n\n-- c\n"},
		{"--[==[\nlong\n]==] x = 1\n", "--[==[\nlong\n]==] x = 1\n"},
		{"-- only a comment", "-- only a comment\n"},
	}
	for _, c := range cases {
		if got := formatSource(t, c.src, format.Options{}); got != c.want {
			t.Errorf("%q: got\n%s\nwant\n%s", c.src, got, c.want)
		}
	}
}

// describe lists the attachments of a comment map in source order, as
// "text: place type".
func describe(cm format.CommentMap) string {
	var parts []string
	byComment := map[*format.Comment]ast.Element{}
	for el, cs := range cm {
		for _, c := range cs {
			byComment[c] = el
		}
	}
	for _, c := range cm.Comments() {
		el := byComment[c]
		s := fmt.Sprintf("%s: %s %s", strings.TrimSpace(c.Text), c.Place, strings.TrimPrefix(fmt.Sprintf("%T", el), "*ast."))
		if c.Place == format.Inner {
			s += fmt.Sprintf(" #%d before %v", c.Index, c.Next)
		}
		if c.SameLine {
			s += " same line"
		}
		if c.BlankBefore {
			s += " after a blank"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n")
}

func commentMap(t *testing.T, src string, d parse.Dialect) format.CommentMap {
	t.Helper()
	chunk, err := parse.ParseChunk([]byte(src), "test", parse.Options{Dialect: d, Tokens: true})
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	return format.NewCommentMap(chunk)
}

// TestCommentMap checks the attachment rules (NewCommentMap).
func TestCommentMap(t *testing.T) {
	cases := []struct {
		src, want string
	}{
		// leading: on the lines above; trailing: on the line it ends
		{"-- a\n\n-- b\nlocal x = 1 -- c\n-- d\n",
			"a: leading LocalAssignStmt\nb: leading LocalAssignStmt after a blank\nc: trailing LocalAssignStmt same line\nd: inner Chunk #1 before <eof>"},
		// the outermost element ending or starting at the token
		{"x = f(a) -- a\n", "a: trailing AssignStmt same line"},
		{"x = a -- a\n + b\n", "a: trailing IdentExpr same line"},
		{"x = --[[a]] a + b\n", "a: leading ArithmeticOpExpr same line"},
		// after a separator: trailing of the item before it
		{"t = {1, -- a\n 2; -- b\n}\nf(x, -- c\n y)\n",
			"a: trailing Field same line\nb: trailing Field same line\nc: trailing IdentExpr same line"},
		{"a(); -- a\n", "a: trailing FuncCallStmt same line"},
		// on the line of an opening token: leading of what follows
		{"t = { -- a\n 1 }\nif x then -- b\n y() end\n",
			"a: leading Field same line\nb: leading FuncCallStmt same line"},
		// on the line of a parameter list: trailing of it
		{"function f() -- a\n return end\n", "a: trailing ParList same line"},
		// dangling: in an empty table, block, argument or parameter list,
		// at the end of a block
		{"t = {--[[a]]}\nf(--[[b]])\nfunction g(--[[c]]) end\n",
			"a: inner TableExpr #0 before } same line\nb: inner FuncCallExpr #1 before ) same line\nc: inner ParList #0 before ) same line"},
		{"do -- a\nend\nwhile x do\n -- b\nend\nrepeat\n y()\n -- c\nuntil x\n",
			"a: inner DoBlockStmt #0 before end same line\nb: inner WhileStmt #1 before end\nc: inner RepeatStmt #1 before until"},
		{"if a then\n x()\n -- a\nelse\n -- b\nend\n",
			"a: inner IfStmt #2 before else\nb: inner IfStmt #2 before end"},
		{"if a then\n -- a\nelseif b then\nend\n", "a: leading IfStmt"},
		// before a keyword or a name held as a string
		{"local --[[a]] x = 1\nfor --[[b]] i = 1, 2 do end\n",
			"a: inner LocalAssignStmt #0 before <name> same line\nb: inner NumberForStmt #0 before <name> same line"},
		{"if a\n-- a\nthen end\n", "a: inner IfStmt #1 before then"},
		// inside parentheses
		{"x = (--[[a]] a --[[b]])\n", "a: leading IdentExpr same line\nb: trailing IdentExpr same line"},
		{"x = ( --[[a]] (a))\n", "a: inner AssignStmt #1 before ( same line"},
		// the end of the chunk, an empty chunk
		{"x = 1\n-- a", "a: inner Chunk #1 before <eof>"},
		{"-- a", "a: inner Chunk #0 before <eof>"},
	}
	for _, c := range cases {
		cm := commentMap(t, c.src, parse.LuaJIT)
		if got := describe(cm); got != c.want {
			t.Errorf("%q: got\n%s\nwant\n%s", c.src, got, c.want)
		}
	}
	if cm := commentMap(t, "x = 1", parse.Lua51); cm != nil {
		t.Errorf("no comments: %v", cm)
	}
}

// TestCommentMapComplete checks that every comment of the comment
// sources and the compatibility scripts is attached once, and printed once.
func TestCommentMapComplete(t *testing.T) {
	srcs := append([]string(nil), commentSources...)
	for _, b := range luaFiles(t, "../testdata/compat") {
		srcs = append(srcs, string(b))
	}
	for _, src := range srcs {
		chunk, err := parse.ParseChunk([]byte(src), "test", parse.Options{Dialect: parse.LuaJIT, Tokens: true, Comments: true})
		if err != nil {
			continue
		}
		cm := format.NewCommentMap(chunk)
		list := cm.Comments()
		if len(list) != len(chunk.Comments) {
			t.Errorf("%.40q: %d comments attached, %d in the source", src, len(list), len(chunk.Comments))
			continue
		}
		for i, c := range list {
			if c.Comment != chunk.Comments[i] {
				t.Errorf("%.40q: comment %d: %+v, parser %+v", src, i, c.Comment, chunk.Comments[i])
			}
		}
		// a comment map printed with a tree without its tokens: every
		// comment once
		chunk.Tokens = nil
		var b bytes.Buffer
		if err := format.Chunk(&b, chunk, cm, format.Options{Dialect: parse.LuaJIT}); err != nil {
			t.Errorf("%.40q: %v", src, err)
			continue
		}
		got := lexemes(b.Bytes(), parse.LuaJIT, ast.TokenComment)
		want := lexemes([]byte(src), parse.LuaJIT, ast.TokenComment)
		if !equalStrings(got, want) {
			t.Errorf("%.40q: printed without tokens: comments %d, want %d: %s", src, len(got), len(want), firstStringDiff(want, got))
		}
	}
}

// TestCommentsOfBuiltTree prints a built tree with comments attached by
// hand, a short one where a line break would be a hazard included.
func TestCommentsOfBuiltTree(t *testing.T) {
	f := id("f")
	c := call(f, id("g"))
	stmts := []ast.Stmt{assign([]ast.Expr{id("x")}, id("y")), callStmt(c)}
	cm := format.CommentMap{
		stmts[0]: {{Comment: ast.Comment{Text: " set x"}, Place: format.Leading}},
		f:        {{Comment: ast.Comment{Text: " the function"}, Place: format.Trailing, SameLine: true, NewlineAfter: true}},
		stmts[1]: {{Comment: ast.Comment{Text: "[[ not long", Long: false}, Place: format.Trailing, SameLine: true}},
	}
	var b bytes.Buffer
	if err := format.Chunk(&b, &ast.Chunk{Stmts: stmts}, cm, format.Options{}); err != nil {
		t.Fatal(err)
	}
	want := "-- set x\nx = y\nf( -- the function\n    g\n) -- [[ not long\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
	if _, err := parse.ParseChunk(b.Bytes(), "test", parse.Options{}); err != nil {
		t.Error(err)
	}
	// a comment of an element not in the tree is an error
	cm[id("z")] = []*format.Comment{{Comment: ast.Comment{Text: "lost"}}}
	if err := format.Chunk(&b, &ast.Chunk{Stmts: stmts}, cm, format.Options{}); err == nil {
		t.Error("no error for a comment not in the tree")
	}
}
