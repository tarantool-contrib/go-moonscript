package parse

// go-moonscript patch: tests of the tokens of a source (ast.Tokens,
// Options.Tokens, Tokenize).

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
)

// describeTokens returns the tokens as "kind" for a keyword or an
// operator whose text is the kind's, else "kind text".
func describeTokens(src []byte, toks ast.Tokens) string {
	var parts []string
	for _, t := range toks {
		text := string(t.Text(src))
		if (t.Kind.IsKeyword() || t.Kind.IsOperator()) && text == t.Kind.String() {
			parts = append(parts, text)
		} else if t.Kind == ast.TokenEOF {
			parts = append(parts, "<eof>")
		} else {
			parts = append(parts, fmt.Sprintf("%v%q", t.Kind, text))
		}
	}
	return strings.Join(parts, " ")
}

func TestTokenKinds(t *testing.T) {
	for k := ast.TokenInvalid; k <= ast.TokenDots; k++ {
		if k.String() == "" || k.String() == "<invalid>" && k != ast.TokenInvalid {
			t.Errorf("kind %d has no name", k)
		}
		n := 0
		for _, is := range []bool{k.IsTrivia(), k.IsKeyword(), k.IsOperator()} {
			if is {
				n++
			}
		}
		if n > 1 {
			t.Errorf("%v: trivia, keyword or operator", k)
		}
	}
	if ast.TokenKind(255).String() != "<invalid>" {
		t.Error(ast.TokenKind(255).String())
	}
	for _, k := range tokenKinds {
		if k == ast.TokenInvalid {
			t.Error("a lexer token has no kind")
		}
	}
	for i := firstReserved; i <= tkWhile; i++ {
		if k := tokenKinds[i]; !k.IsKeyword() || k.String() != tokenNames[i-firstReserved] {
			t.Errorf("%s: %v", tokenNames[i-firstReserved], k)
		}
	}
	for i := tkConcat; i <= tkLabel; i++ {
		if k := tokenKinds[i]; !k.IsOperator() || k.String() != tokenNames[i-firstReserved] {
			t.Errorf("%s: %v", tokenNames[i-firstReserved], k)
		}
	}
	for _, c := range "+-*/%^#<>=(){}[];:,." {
		if k := tokenKinds[c]; !k.IsOperator() || k.String() != string(c) {
			t.Errorf("%c: %v", c, k)
		}
	}
	for _, c := range []byte("~@$!?&|`\\\x00\x7f\x80") {
		if k := tokenKinds[c]; k != ast.TokenOther {
			t.Errorf("%q: %v", c, k)
		}
	}
	for _, c := range []struct {
		text  string
		level int
	}{{"[[", 0}, {"[=[", 1}, {"[==[x", 2}, {"[", -1}, {"[=", -1}, {"[=]", -1}, {"", -1}, {"x[[", -1}, {"[ [", -1}} {
		if got := ast.LongBracketLevel([]byte(c.text)); got != c.level {
			t.Errorf("LongBracketLevel(%q) = %d", c.text, got)
		}
	}
}

// TestTokens checks the tokens of sources that parse: line breaks, white
// space, strings, long brackets, comments and headers.
func TestTokens(t *testing.T) {
	jit := Options{Dialect: LuaJIT}
	l51 := Options{Dialect: Lua51}
	l51sh := Options{Dialect: Lua51, Shebang: true}
	for _, c := range []struct {
		src  string
		opts []Options
		want string
	}{
		{"", nil, `<eof>`},
		{" \t\f\v", nil, `<whitespace>" \t\f\v" <eof>`},
		{"x = 1 \t", nil, `<name>"x" <whitespace>" " = <whitespace>" " <number>"1" <whitespace>" \t" <eof>`},
		{"a()\r\nb()\rc()\n\rd()\n\ne()\n\r\n", nil, `<name>"a" ( ) <newline>"\r\n" <name>"b" ( ) <newline>"\r" ` +
			`<name>"c" ( ) <newline>"\n\r" <name>"d" ( ) <newline>"\n" <newline>"\n" <name>"e" ( ) <newline>"\n\r" <newline>"\n" <eof>`},
		{"x = 1 -- one\n\n-- two\ny = 2", nil, `<name>"x" <whitespace>" " = <whitespace>" " <number>"1" <whitespace>" " ` +
			`<comment>"-- one" <newline>"\n" <newline>"\n" <comment>"-- two" <newline>"\n" <name>"y" <whitespace>" " = <whitespace>" " <number>"2" <eof>`},
		{"-- a\r\n--[[b\r\nc]]--[=[d]=]", nil, `<comment>"-- a" <newline>"\r\n" <comment>"--[[b\r\nc]]" <comment>"--[=[d]=]" <eof>`},
		{"--", nil, `<comment>"--" <eof>`},
		{"--[==[ ]] ]=] ]==] --[ x\n---[[ y", nil, `<comment>"--[==[ ]] ]=] ]==]" <whitespace>" " <comment>"--[ x" <newline>"\n" <comment>"---[[ y" <eof>`},
		{"--[[ --[[ ]] x()", []Options{jit}, `<comment>"--[[ --[[ ]]" <whitespace>" " <name>"x" ( ) <eof>`},
		{"--[=[ [[ ]] ]=]", nil, `<comment>"--[=[ [[ ]] ]=]" <eof>`},
		{"--[[ [[ ]]", []Options{jit}, `<comment>"--[[ [[ ]]" <eof>`},
		{"s = [[a]] .. [=[b]]]=] .. [==[\r\nc]==] .. [===[]===]", nil, `<name>"s" <whitespace>" " = <whitespace>" " ` +
			`<string>"[[a]]" <whitespace>" " .. <whitespace>" " <string>"[=[b]]]=]" <whitespace>" " .. <whitespace>" " ` +
			`<string>"[==[\r\nc]==]" <whitespace>" " .. <whitespace>" " <string>"[===[]===]" <eof>`},
		{"s = 'a' .. \"b\" .. 'c\\\nd' .. \"e\\\r\nf\"", nil, `<name>"s" <whitespace>" " = <whitespace>" " <string>"'a'" ` +
			`<whitespace>" " .. <whitespace>" " <string>"\"b\"" <whitespace>" " .. <whitespace>" " <string>"'c\\\nd'" ` +
			`<whitespace>" " .. <whitespace>" " <string>"\"e\\\r\nf\"" <eof>`},
		{"s = 'a\\z\n   b'", []Options{jit}, `<name>"s" <whitespace>" " = <whitespace>" " <string>"'a\\z\n   b'" <eof>`},
		{"x = a.\x00b", []Options{l51}, `<name>"x" <whitespace>" " = <whitespace>" " <name>"a" ..".\x00" <name>"b" <eof>`},
		{"::l:: goto l", []Options{jit}, `:: <name>"l" :: <whitespace>" " goto <whitespace>" " <name>"l" <eof>`},
		{"goto = 1", []Options{l51}, `<name>"goto" <whitespace>" " = <whitespace>" " <number>"1" <eof>`},
		{"return ...;", nil, `return <whitespace>" " ... ; <eof>`},
		{"x = 0x1p4 + 1LL", []Options{jit}, `<name>"x" <whitespace>" " = <whitespace>" " <number>"0x1p4" <whitespace>" " + <whitespace>" " <number>"1LL" <eof>`},
		{"#!/usr/bin/lua\nx()", []Options{jit, l51sh}, `<header>"#!/usr/bin/lua" <newline>"\n" <name>"x" ( ) <eof>`},
		{"#!lua\r\n\r\nx()", []Options{jit, l51sh}, `<header>"#!lua" <newline>"\r\n" <newline>"\r\n" <name>"x" ( ) <eof>`},
		{"#", []Options{jit, l51sh}, `<header>"#" <eof>`},
		{"#\n", []Options{jit, l51sh}, `<header>"#" <newline>"\n" <eof>`},
		{"\xef\xbb\xbfx()", []Options{jit}, `<header>"\ufeff" <name>"x" ( ) <eof>`},
		{"\xef\xbb\xbf", []Options{jit}, `<header>"\ufeff" <eof>`},
		{"\xef\xbb\xbf#!lua\n-- c", []Options{jit}, `<header>"\ufeff#!lua" <newline>"\n" <comment>"-- c" <eof>`},
	} {
		opts := c.opts
		if opts == nil {
			opts = []Options{jit, l51}
		}
		for _, o := range opts {
			o.Tokens, o.Comments = true, true
			chunk, err := ParseChunk([]byte(c.src), "src", o)
			if err != nil {
				t.Errorf("%q (%s): %v", c.src, dialectName(o.Dialect), err)
				continue
			}
			if got := describeTokens([]byte(c.src), chunk.Tokens); got != c.want {
				t.Errorf("%q (%s):\n got %s\nwant %s", c.src, dialectName(o.Dialect), got, c.want)
			}
			checkTokens(t, fmt.Sprintf("%q", c.src), []byte(c.src), o, chunk)
		}
	}
}

// TestTokenize checks Tokenize on sources that do not lex or parse: the
// tokens up to the error, and the error lua5.1 or LuaJIT give.
func TestTokenize(t *testing.T) {
	jit := Options{Dialect: LuaJIT}
	l51 := Options{Dialect: Lua51}
	for _, c := range []struct {
		src  string
		opts []Options
		want string
		err  string
	}{
		{"x = = @ $", nil, `<name>"x" <whitespace>" " = <whitespace>" " = <whitespace>" " <other>"@" <whitespace>" " <other>"$" <eof>`, ""},
		{"if ~ then", nil, `if <whitespace>" " <other>"~" <whitespace>" " then <eof>`, ""},
		{"a::b", []Options{l51}, `<name>"a" : : <name>"b" <eof>`, ""},
		{"x = 'abc\ny", nil, `<name>"x" <whitespace>" " = <whitespace>" "`, `src:1: unfinished string near ''abc'`},
		{"x = 1\n  \"abc", nil, `<name>"x" <whitespace>" " = <whitespace>" " <number>"1" <newline>"\n" <whitespace>"  "`,
			`src:2: unfinished string near '<eof>'`},
		{"x = [==[\nabc]=]", nil, `<name>"x" <whitespace>" " = <whitespace>" "`, `src:2: unfinished long string near '<eof>'`},
		{"x = [=x", nil, `<name>"x" <whitespace>" " = <whitespace>" "`, `src:1: invalid long string delimiter near '[='`},
		{"\n--[[ x", nil, `<newline>"\n"`, `src:2: unfinished long comment near '<eof>'`},
		{"--[[ [[ ]]", []Options{l51}, ``, `src:1: nesting of [[...]] is deprecated near '['`},
		{"x = 3x", nil, `<name>"x" <whitespace>" " = <whitespace>" "`, `src:1: malformed number near '3x'`},
		{"s = 'a\\z\n   b'", []Options{l51}, `<name>"s" <whitespace>" " = <whitespace>" "`, `src:1: unfinished string near ''az'`},
		{"s = 'a\\q'", []Options{jit}, `<name>"s" <whitespace>" " = <whitespace>" "`, `src:1: invalid escape sequence near ''a'`},
		{"#!lua\n\x1b", []Options{jit}, `<header>"#!lua" <newline>"\n"`, `cannot load malformed bytecode`},
		{"#!lua\nx", []Options{l51}, `# <other>"!" <name>"lua" <newline>"\n" <name>"x" <eof>`, ""},
		{"\xef\xbb\xbfx", []Options{l51}, `<other>"\xef" <other>"\xbb" <other>"\xbf" <name>"x" <eof>`, ""},
	} {
		opts := c.opts
		if opts == nil {
			opts = []Options{jit, l51}
		}
		for _, o := range opts {
			toks, err := checkTokenize(t, "src", []byte(c.src), o)
			if got := describeTokens([]byte(c.src), toks); got != c.want {
				t.Errorf("%q (%s):\n got %s\nwant %s", c.src, dialectName(o.Dialect), got, c.want)
			}
			if got := fmt.Sprint(err); err == nil && c.err != "" || err != nil && got != c.err {
				t.Errorf("%q (%s): error %v, want %s", c.src, dialectName(o.Dialect), err, c.err)
			}
			if err != nil {
				o.Tokens = true
				if _, perr := ParseChunk([]byte(c.src), "src", o); perr == nil || perr.Error() != err.Error() {
					t.Errorf("%q (%s): ParseChunk: %v", c.src, dialectName(o.Dialect), perr)
				}
			}
		}
	}
}

func TestTokenRange(t *testing.T) {
	src := []byte("local a = (b) -- c\n\nreturn a")
	chunk, err := ParseChunk(src, "src", Options{Tokens: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := chunk.Tokens
	local := chunk.Stmts[0].(*ast.LocalAssignStmt)
	for _, c := range []struct {
		what        string
		first, last int
		want        string
	}{
		{"chunk", 0, len(ts) - 2, ""},
		{"local", 0, 8, "local a = (b)"},
		{"b", 7, 7, "b"},
		{"return", 13, 15, "return a"},
	} {
		var first, last int
		switch c.what {
		case "chunk":
			first, last = chunk.TokenRange(chunk)
		case "local":
			first, last = chunk.TokenRange(local)
		case "b":
			first, last = chunk.TokenRange(local.Exprs[0])
		case "return":
			first, last = chunk.TokenRange(chunk.Stmts[1])
		}
		if first != c.first || last != c.last {
			t.Errorf("%s: range %d-%d, want %d-%d", c.what, first, last, c.first, c.last)
		}
		if c.want != "" {
			if got := string(src[ts[first].Span().Start:ts[last].Span().End]); got != c.want {
				t.Errorf("%s: %q", c.what, got)
			}
		}
	}
	// empty spans and offsets out of the tokens
	if first, last := ts.Range(ast.Span{Start: len(src), End: len(src)}); first != len(ts)-1 || last != first-1 {
		t.Errorf("empty span at the end: %d-%d", first, last)
	}
	if first, last := ts.Range(ast.Span{Start: 6, End: 6}); first != 2 || last != 1 {
		t.Errorf("empty span at a: %d-%d", first, last)
	}
	for _, c := range []struct{ offset, index int }{
		{-1, -1}, {0, 0}, {4, 0}, {5, 1}, {len(src), len(ts) - 1}, {len(src) + 1, -1},
	} {
		if got := ts.At(c.offset); got != c.index {
			t.Errorf("At(%d) = %d, want %d", c.offset, got, c.index)
		}
	}
	if got := (ast.Tokens(nil)).At(0); got != -1 {
		t.Errorf("At on no tokens: %d", got)
	}
	if first, last := (ast.Tokens(nil)).Range(ast.Span{Start: 0, End: 3}); first != 0 || last != -1 {
		t.Errorf("Range on no tokens: %d-%d", first, last)
	}
	if l := ast.NewLexeme(ast.TokenName, ast.Span{Start: 3, End: 1}); l.Span() != (ast.Span{}) || l.Kind != ast.TokenName {
		t.Errorf("NewLexeme of no span: %v", l.Span())
	}
}

// checkTokenFiles parses the files in both dialects with their tokens and
// checks them, and tokenizes the files that do not parse.
func checkTokenFiles(t *testing.T, files map[string][]byte) {
	for _, d := range bothDialects {
		var nfiles, ntokens, trivia, failed, lexed int
		size := 0
		for name, src := range files {
			opts := Options{Dialect: d, Comments: true, Shebang: true, Tokens: true}
			c, err := ParseChunk(src, name, opts)
			if err != nil {
				failed++
				if _, terr := checkTokenize(t, name, src, opts); terr == nil {
					lexed++
				}
				continue
			}
			if checkTokens(t, name, src, opts, c) > 0 {
				continue
			}
			nfiles++
			size += len(src)
			ntokens += len(c.Tokens)
			for _, tk := range c.Tokens {
				if tk.Kind.IsTrivia() {
					trivia++
				}
			}
		}
		t.Logf("%s: %d files, %d bytes: %d tokens, %d trivia; %d files do not parse (%d of them lex)",
			dialectName(d), nfiles, size, ntokens, trivia, failed, lexed)
	}
}

// TestTokensCompat checks the tokens of gopher-lua's test scripts and the
// compatibility scripts.
func TestTokensCompat(t *testing.T) {
	files := luaFiles(t, filepath.Join("..", "testdata"))
	if len(files) == 0 {
		t.Fatal("no scripts")
	}
	checkTokenFiles(t, files)
}

// TestTokensArchives checks the tokens of the Lua files of the archives.
func TestTokensArchives(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join(archivesDir, "*.tar.gz"))
	if len(paths) == 0 {
		t.Skip("no archives in", archivesDir)
	}
	if testing.Short() {
		t.Skip("short")
	}
	files := map[string][]byte{}
	for _, p := range paths {
		if err := readTarGz(p, files); err != nil {
			t.Fatal(err)
		}
	}
	checkTokenFiles(t, files)
}

// FuzzTokens tokenizes and parses random sources with their tokens: the
// tokens cover the source (or its start, up to a lexical error), those of
// the grammar are the lexer's, the token range of each element matches
// its span, and the tree is the one parsed without them.
func FuzzTokens(f *testing.F) {
	for _, s := range []string{
		"local a, b = (f()), {x = 1, [2] = 'y'; 3} -- c\n\n-- d\nreturn",
		"--[==[\r\nlong\r\n]==]\nfunction a.b:c(d, ...) return ((d)) end",
		"#!/bin/lua\r\nfor i = 1, 2 do ::l:: goto l end  ",
		"\xef\xbb\xbfs = 'a\\z\n  b' .. [[\n]] .. \"\\\r\n\"",
		"x = 0x1p4 + 1e3 + 1LL + .5 - -#t\n\r--x\rreturn",
		"--[[ --[[ ]] x = [=[ ]] ]=] \t\v\f",
		"x = 'unfinished",
		"",
	} {
		f.Add([]byte(s))
	}
	for name, src := range luaFiles(f, filepath.Join("..", "testdata", "compat")) {
		if len(src) < 2000 && !strings.Contains(name, "limits") {
			f.Add(src)
		}
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		for _, d := range bothDialects {
			for _, shebang := range []bool{false, true} {
				opts := Options{Dialect: d, Comments: true, Shebang: shebang, Tokens: true}
				toks, terr := checkTokenize(t, "fuzz", src, opts)
				c, err := ParseChunk(src, "fuzz", opts)
				if err != nil {
					continue
				}
				if !slices.Equal(toks, c.Tokens) {
					t.Fatalf("%v: Tokenize reads other tokens", d)
				}
				if terr != nil {
					t.Fatalf("%v: parses, but Tokenize fails: %v", d, terr)
				}
				checkTokens(t, "fuzz", src, opts, c)
			}
		}
	})
}
