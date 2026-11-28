package lex_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

// tok is a token as the tests spell it: its text instead of its offsets.
type tok struct {
	Kind    lex.Kind
	Text    string
	Line    int
	EndLine int
}

// on is a token that starts and ends on line.
func on(kind lex.Kind, text string, line int) tok {
	return tok{Kind: kind, Text: text, Line: line, EndLine: line}
}

// name, number, symbol are tokens of their kind on line 1.
func name(text string) tok   { return on(lex.Name, text, 1) }
func number(text string) tok { return on(lex.Number, text, 1) }
func symbol(text string) tok { return on(lex.Symbol, text, 1) }

// testTokens reads every token of src with the lexer and spells them as tok.
func testTokens(t *testing.T, lexer *lex.Lexer, src string) []tok {
	t.Helper()

	tokens, err := lexer.Tokens()
	require.NoError(t, err, "lex %q", src)

	spelled := make([]tok, 0, len(tokens))
	for _, token := range tokens {
		spelled = append(spelled, tok{
			Kind: token.Kind, Text: src[token.Start:token.End], Line: token.Line, EndLine: token.EndLine,
		})
	}

	return spelled
}

// lexCase is one source and the tokens it must read as.
type lexCase struct {
	name   string
	src    string
	tokens []tok
}

// testLexCases checks every case with a lexer from lex.New.
func testLexCases(t *testing.T, tests []lexCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.tokens, testTokens(t, lex.New([]byte(tt.src)), tt.src))
		})
	}
}

func TestLexer_Tokens_Names(t *testing.T) {
	t.Parallel()

	testLexCases(t, []lexCase{
		{
			name:   "identifiers and reserved words",
			src:    "local x_1 = _ENV",
			tokens: []tok{name("local"), name("x_1"), symbol("="), name("_ENV")},
		},
		{
			name:   "words reserved in some dialects only",
			src:    "goto continue; global print",
			tokens: []tok{name("goto"), name("continue"), symbol(";"), name("global"), name("print")},
		},
		{
			// LuaJIT reads bytes 0x80 to 0xFF as identifier characters.
			name:   "UTF-8 identifier (LuaJIT)",
			src:    "local café = λ1",
			tokens: []tok{name("local"), name("café"), symbol("="), name("λ1")},
		},
		{
			name:   "first and last letters and digits",
			src:    "AZ az _09 Zz",
			tokens: []tok{name("AZ"), name("az"), name("_09"), name("Zz")},
		},
		{
			// A name may start with either end of the range, and hold both.
			name:   "bytes 0x80 and 0xFF (LuaJIT)",
			src:    "\x80 \xff a\x80\xff",
			tokens: []tok{name("\x80"), name("\xff"), name("a\x80\xff")},
		},
	})
}

func TestLexer_Tokens_Numbers(t *testing.T) {
	t.Parallel()

	numbers := []string{
		// The examples of the Lua manual, section 2.1 of the 5.1 manual and 3.1 of later ones.
		"3", "345", "0xff", "0xBEBADA", "3.0", "3.1416", "314.16e-2", "0.31416E1", "34e1",
		"0x0.1E", "0xA23p-4", "0X1.921FB54442D18P+1",
		// A fraction or an exponent alone.
		".5", "5.", "1e+10", "1E-10", ".5e-3",
		// LuaJIT: 64-bit integers and imaginary numbers.
		"1LL", "1ULL", "0x10LL", "0xffULL", "2i", "12.5i",
	}

	tests := make([]lexCase, 0, len(numbers))
	for _, text := range numbers {
		tests = append(tests, lexCase{name: text, src: text, tokens: []tok{number(text)}})
	}

	tests = append(tests,
		lexCase{
			name:   "signs after an exponent letter only",
			src:    "1e-1-1+x",
			tokens: []tok{number("1e-1"), symbol("-"), number("1"), symbol("+"), name("x")},
		},
		lexCase{
			// In a hexadecimal numeral e is a digit and p the exponent letter.
			name:   "hexadecimal e is no exponent",
			src:    "0x1e-1",
			tokens: []tok{number("0x1e"), symbol("-"), number("1")},
		},
		lexCase{
			name:   "concatenation between spaced numerals",
			src:    "1 .. 2",
			tokens: []tok{number("1"), symbol(".."), number("2")},
		},
		lexCase{
			// Every dialect reads a malformed numeral here, so no tool may
			// write "1 .. 2" as "1..2".
			name:   "dots run into a numeral",
			src:    "1..2",
			tokens: []tok{number("1..2")},
		},
		lexCase{
			// Lua 5.1 reads 1e5 .. x; LuaJIT a malformed numeral.
			name:   "numeral with an exponent before dots",
			src:    "1e5..x",
			tokens: []tok{number("1e5..x")},
		},
		lexCase{
			// Every supported dialect reads a malformed numeral here; it is a
			// token all the same, for a parser to refuse.
			name:   "numeral touching a letter",
			src:    "3then",
			tokens: []tok{number("3then")},
		},
	)

	testLexCases(t, tests)
}

func TestLexer_Tokens_ShortStrings(t *testing.T) {
	t.Parallel()

	testLexCases(t, []lexCase{
		{
			name:   "quotes of the other kind",
			src:    `"it's" 'say "hi"'`,
			tokens: []tok{on(lex.String, `"it's"`, 1), on(lex.String, `'say "hi"'`, 1)},
		},
		{
			name:   "escaped quotes",
			src:    `"\"" '\''`,
			tokens: []tok{on(lex.String, `"\""`, 1), on(lex.String, `'\''`, 1)},
		},
		{
			// The second backslash is escaped, so the quote after it ends the
			// string.
			name:   "string ending in an escaped backslash",
			src:    `"\\" "|    |"`,
			tokens: []tok{on(lex.String, `"\\"`, 1), on(lex.String, `"|    |"`, 1)},
		},
		{
			name:   "escaped backslash before an escaped quote",
			src:    `"\\\"" x`,
			tokens: []tok{on(lex.String, `"\\\""`, 1), name("x")},
		},
		{
			// LuaJIT; Lua 5.1 reads x41, u{48} and "A".
			name:   "hexadecimal, Unicode and decimal escapes",
			src:    `"\x41\u{48}\065"`,
			tokens: []tok{on(lex.String, `"\x41\u{48}\065"`, 1)},
		},
		{
			name:   "comment and long bracket inside a string",
			src:    `"-- [[" x`,
			tokens: []tok{on(lex.String, `"-- [["`, 1), name("x")},
		},
		{
			name:   "escaped line break",
			src:    "\"a\\\nb\" x",
			tokens: []tok{{Kind: lex.String, Text: "\"a\\\nb\"", Line: 1, EndLine: 2}, on(lex.Name, "x", 2)},
		},
		{
			name:   "escaped CR LF is one line break",
			src:    "\"a\\\r\nb\" x",
			tokens: []tok{{Kind: lex.String, Text: "\"a\\\r\nb\"", Line: 1, EndLine: 2}, on(lex.Name, "x", 2)},
		},
		{
			name:   "escaped LF CR is one line break",
			src:    "\"a\\\n\rb\" x",
			tokens: []tok{{Kind: lex.String, Text: "\"a\\\n\rb\"", Line: 1, EndLine: 2}, on(lex.Name, "x", 2)},
		},
		{
			name:   "escaped CR",
			src:    "'a\\\rb' x",
			tokens: []tok{{Kind: lex.String, Text: "'a\\\rb'", Line: 1, EndLine: 2}, on(lex.Name, "x", 2)},
		},
		{
			// LuaJIT: \z skips white space, line breaks included.
			name: "z escape over line breaks",
			src:  "\"a\\z\n  \r\n\f\v b\" x",
			tokens: []tok{
				{Kind: lex.String, Text: "\"a\\z\n  \r\n\f\v b\"", Line: 1, EndLine: 3},
				on(lex.Name, "x", 3),
			},
		},
		{
			name:   "z escape before the closing quote",
			src:    "'\\z  ' x",
			tokens: []tok{on(lex.String, "'\\z  '", 1), name("x")},
		},
	})
}

func TestLexer_Tokens_LongBrackets(t *testing.T) {
	t.Parallel()

	testLexCases(t, []lexCase{
		{
			name:   "levels 0 and 1",
			src:    "[[a]] [=[b]=]",
			tokens: []tok{on(lex.LongString, "[[a]]", 1), on(lex.LongString, "[=[b]=]", 1)},
		},
		{
			name:   "empty",
			src:    "[[]][==[]==]",
			tokens: []tok{on(lex.LongString, "[[]]", 1), on(lex.LongString, "[==[]==]", 1)},
		},
		{
			name:   "closers of other levels inside",
			src:    "[==[a]]b]=]c]===]d]==] x",
			tokens: []tok{on(lex.LongString, "[==[a]]b]=]c]===]d]==]", 1), name("x")},
		},
		{
			name:   "comment start and quote inside a leveled string",
			src:    "[==[http://example.com/a--b\"]==] x",
			tokens: []tok{on(lex.LongString, "[==[http://example.com/a--b\"]==]", 1), name("x")},
		},
		{
			// LuaJIT closes at the first "]]"; Lua 5.1 refuses the nested
			// opener.
			name:   "nested opener is no nesting",
			src:    "[[ [[ ]] x = \"]]\"",
			tokens: []tok{on(lex.LongString, "[[ [[ ]]", 1), name("x"), symbol("="), on(lex.String, "\"]]\"", 1)},
		},
		{
			name:   "closing bracket after the closer",
			src:    "[[a]]]",
			tokens: []tok{on(lex.LongString, "[[a]]", 1), symbol("]")},
		},
		{
			// A closer is "]", the "=" signs and "]" again: "]=[" closes nothing.
			name:   "closer ending in an opening bracket",
			src:    "[=[a]=[b]=] --[=[c]=[d]=] x",
			tokens: []tok{on(lex.LongString, "[=[a]=[b]=]", 1), on(lex.LongComment, "--[=[c]=[d]=]", 1), name("x")},
		},
		{
			name: "every kind of line break inside",
			src:  "[[a\r\nb\n\rc\rd\ne]] x",
			tokens: []tok{
				{Kind: lex.LongString, Text: "[[a\r\nb\n\rc\rd\ne]]", Line: 1, EndLine: 5},
				on(lex.Name, "x", 5),
			},
		},
		{
			// A long string right after a name is a call argument, not an index.
			name: "call with a long string",
			src:  "t[[x]] t[ [[x]] ]",
			tokens: []tok{
				name("t"), on(lex.LongString, "[[x]]", 1),
				name("t"), symbol("["), on(lex.LongString, "[[x]]", 1), symbol("]"),
			},
		},
	})
}

func TestLexer_Tokens_Comments(t *testing.T) {
	t.Parallel()

	testLexCases(t, []lexCase{
		{
			name:   "short comment without its line break",
			src:    "a -- c\nb",
			tokens: []tok{name("a"), on(lex.Comment, "-- c", 1), on(lex.Name, "b", 2)},
		},
		{
			name:   "short comment ended by CR",
			src:    "a -- c\rb",
			tokens: []tok{name("a"), on(lex.Comment, "-- c", 1), on(lex.Name, "b", 2)},
		},
		{
			name:   "short comment at the end of the source",
			src:    "a --",
			tokens: []tok{name("a"), on(lex.Comment, "--", 1)},
		},
		{
			name: "long comments",
			src:  "--[[c]]a--[==[ ]] ]=] ]==]b",
			tokens: []tok{
				on(lex.LongComment, "--[[c]]", 1), name("a"),
				on(lex.LongComment, "--[==[ ]] ]=] ]==]", 1), name("b"),
			},
		},
		{
			name:   "multi-line long comment",
			src:    "--[[\n\r\n]]x",
			tokens: []tok{{Kind: lex.LongComment, Text: "--[[\n\r\n]]", Line: 1, EndLine: 3}, on(lex.Name, "x", 3)},
		},
		{
			// An opener that is not one starts a short comment.
			name: "broken openers",
			src:  "--[=x ]]\n--[\n--[ [\ny",
			tokens: []tok{
				on(lex.Comment, "--[=x ]]", 1), on(lex.Comment, "--[", 2), on(lex.Comment, "--[ [", 3),
				on(lex.Name, "y", 4),
			},
		},
		{
			name:   "third dash makes a short comment",
			src:    "---[[c]] x\ny",
			tokens: []tok{on(lex.Comment, "---[[c]] x", 1), on(lex.Name, "y", 2)},
		},
		{
			name:   "comment glued between tokens",
			src:    "local--[[c]]b",
			tokens: []tok{name("local"), on(lex.LongComment, "--[[c]]", 1), name("b")},
		},
	})
}

func TestLexer_Tokens_Symbols(t *testing.T) {
	t.Parallel()

	symbols := []string{
		"+", "-", "*", "/", "%", "^", "#",
		"==", "~=", "<=", ">=", "<", ">", "=", "(", ")", "{", "}", "[", "]",
		"::", ";", ":", ",", ".", "..", "...",
	}

	tests := make([]lexCase, 0, len(symbols))
	for _, text := range symbols {
		tests = append(tests, lexCase{name: text, src: text, tokens: []tok{symbol(text)}})
	}

	invalid := func(text string) tok { return on(lex.Invalid, text, 1) }

	tests = append(tests,
		lexCase{
			name: "unspaced operators",
			src:  "a<=b<c~=d>=e...f",
			tokens: []tok{
				name("a"), symbol("<="), name("b"), symbol("<"), name("c"), symbol("~="), name("d"),
				symbol(">="), name("e"), symbol("..."), name("f"),
			},
		},
		lexCase{
			name:   "label",
			src:    "::top::",
			tokens: []tok{symbol("::"), name("top"), symbol("::")},
		},
		lexCase{
			name: "bytes that start no token",
			src:  "$!?@`\\\x00\x7f&|~",
			tokens: []tok{
				invalid("$"), invalid("!"), invalid("?"), invalid("@"),
				invalid("`"), invalid("\\"), invalid("\x00"), invalid("\x7f"),
				invalid("&"), invalid("|"), invalid("~"),
			},
		},
	)

	testLexCases(t, tests)
}

// TestLexer_Tokens_NoOperatorsOfLua53: the operators of Lua 5.3 are no tokens
// of a supported dialect: "//", "<<" and ">>" are the symbols they are made
// of, and "&", "|" and "~" are bytes that start no token.
func TestLexer_Tokens_NoOperatorsOfLua53(t *testing.T) {
	t.Parallel()

	invalid := func(text string) tok { return on(lex.Invalid, text, 1) }

	testLexCases(t, []lexCase{
		{name: "floor division", src: "a//b", tokens: []tok{name("a"), symbol("/"), symbol("/"), name("b")}},
		{name: "shifts", src: "a<<b>>c", tokens: []tok{
			name("a"), symbol("<"), symbol("<"), name("b"), symbol(">"), symbol(">"), name("c"),
		}},
		{name: "shift assignments", src: "a<<=b>>=c", tokens: []tok{
			name("a"), symbol("<"), symbol("<="), name("b"), symbol(">"), symbol(">="), name("c"),
		}},
		{name: "bitwise operators", src: "a&b|c~d", tokens: []tok{
			name("a"), invalid("&"), name("b"), invalid("|"), name("c"), invalid("~"), name("d"),
		}},
	})
}

// TestLexer_Tokens_Lua51Only: a token that LuaJIT's lexer refuses and Lua 5.1
// reads is read whole, and the lexer reads on after it, as from the start of
// a file but for its header: a "#" or a byte order mark there is a token.
func TestLexer_Tokens_Lua51Only(t *testing.T) {
	t.Parallel()

	testLexCases(t, []lexCase{
		{
			name: "escapes only Lua 5.1 reads", src: `f("\.", '\%', "\x4", "\u{D800}") x`,
			tokens: []tok{
				name("f"), symbol("("), on(lex.String, `"\."`, 1), symbol(","), on(lex.String, `'\%'`, 1),
				symbol(","), on(lex.String, `"\x4"`, 1), symbol(","), on(lex.String, `"\u{D800}"`, 1),
				symbol(")"), name("x"),
			},
		},
		{
			name: "a refused string over lines", src: "'\\q\\\n\\z\n  x' y",
			tokens: []tok{{Kind: lex.String, Text: "'\\q\\\n\\z\n  x'", Line: 1, EndLine: 3}, on(lex.Name, "y", 3)},
		},
		{
			name: "numerals Lua 5.1 ends", src: "1e5..x 3then y 0x10else",
			tokens: []tok{number("1e5..x"), number("3then"), name("y"), number("0x10else")},
		},
		{
			name: "a hash after a refused token", src: `'\q'#t`,
			tokens: []tok{on(lex.String, `'\q'`, 1), symbol("#"), name("t")},
		},
		{
			name: "a byte order mark after a refused numeral", src: "1e5..\xEF\xBB\xBFx#",
			tokens: []tok{number("1e5..\xEF\xBB\xBFx"), symbol("#")},
		},
		{
			name: "a byte order mark after a refused string", src: "'\\q'\xEF\xBB\xBFx #",
			tokens: []tok{on(lex.String, `'\q'`, 1), name("\xEF\xBB\xBFx"), symbol("#")},
		},
		{
			name: "LuaJIT's own", src: "\"\\x41\\z  \" 0x1p4 1ULL \xD0\xBF",
			tokens: []tok{on(lex.String, "\"\\x41\\z  \"", 1), number("0x1p4"), number("1ULL"), name("\xD0\xBF")},
		},
	})
}

func TestLexer_Tokens_LineBreaks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		line int
	}{
		{name: "LF", src: "a\nb", line: 2},
		{name: "CR", src: "a\rb", line: 2},
		{name: "CR LF", src: "a\r\nb", line: 2},
		{name: "LF CR", src: "a\n\rb", line: 2},
		{name: "two LFs", src: "a\n\nb", line: 3},
		{name: "two CRs", src: "a\r\rb", line: 3},
		{name: "LF CR, then LF", src: "a\n\r\nb", line: 3},
		{name: "CR LF, then CR", src: "a\r\n\rb", line: 3},
		{name: "two CR LFs", src: "a\r\n\r\nb", line: 3},
		{name: "form feed and vertical tab are no line breaks", src: "a\f\v\tb", line: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want := []tok{name("a"), on(lex.Name, "b", tt.line)}
			assert.Equal(t, want, testTokens(t, lex.New([]byte(tt.src)), tt.src))
		})
	}
}

func TestLexer_Next_EOF(t *testing.T) {
	t.Parallel()

	lexer := lex.New([]byte("x\n\n"))

	first, err := lexer.Next()
	require.NoError(t, err)
	assert.Equal(t, lex.Token{Kind: lex.Name, Start: 0, End: 1, Line: 1, EndLine: 1}, first)

	for range 2 {
		eof, err := lexer.Next()
		require.NoError(t, err)
		assert.Equal(t, lex.Token{Kind: lex.EOF, Start: 3, End: 3, Line: 3, EndLine: 3}, eof,
			"EOF is at the end of the source, on its last line, every time")
	}
}

func TestLexer_Next_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		src     string
		err     error
		offset  int
		line    int
		message string
	}{
		{
			name: "string at the end of the source", src: "x = \"abc",
			err: lex.ErrUnfinishedString, offset: 4, line: 1, message: "line 1: unfinished string",
		},
		{
			name: "line break in a string", src: "x = 'abc\ny'",
			err: lex.ErrUnfinishedString, offset: 4, line: 1, message: "line 1: unfinished string",
		},
		{
			name: "CR in a string", src: "x = 'abc\ry'",
			err: lex.ErrUnfinishedString, offset: 4, line: 1, message: "line 1: unfinished string",
		},
		{
			name: "backslash at the end of the source", src: "\n\"abc\\",
			err: lex.ErrUnfinishedString, offset: 1, line: 2, message: "line 2: unfinished string",
		},
		{
			name: "long string", src: "x = [==[abc]=]",
			err: lex.ErrUnfinishedLongString, offset: 4, line: 1, message: "line 1: unfinished long string",
		},
		{
			name: "long comment", src: "\n\n--[[abc",
			err: lex.ErrUnfinishedLongComment, offset: 2, line: 3, message: "line 3: unfinished long comment",
		},
		{
			name: "equals sign after a bracket", src: "t[=1]",
			err: lex.ErrLongBracket, offset: 1, line: 1, message: "line 1: invalid long string delimiter",
		},
		{
			name: "equals signs at the end of the source", src: "[==",
			err: lex.ErrLongBracket, offset: 0, line: 1, message: "line 1: invalid long string delimiter",
		},
		{
			// An opener is "[", the "=" signs and "[" again.
			name: "equals sign before a closing bracket", src: "t[=]",
			err: lex.ErrLongBracket, offset: 1, line: 1, message: "line 1: invalid long string delimiter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := lex.New([]byte(tt.src)).Tokens()
			require.ErrorIs(t, err, tt.err)
			require.EqualError(t, err, tt.message)

			lexErr, ok := errors.AsType[*lex.Error](err)
			require.True(t, ok, "the error is a *lex.Error")
			assert.Equal(t, tt.offset, lexErr.Offset)
			assert.Equal(t, tt.line, lexErr.Line)
		})
	}
}

func TestLexer_Next_ErrorSticks(t *testing.T) {
	t.Parallel()

	lexer := lex.New([]byte("a 'b\nc"))

	first, err := lexer.Next()
	require.NoError(t, err)
	assert.Equal(t, lex.Name, first.Kind)

	_, err = lexer.Next()
	require.ErrorIs(t, err, lex.ErrUnfinishedString)

	_, again := lexer.Next()
	assert.Same(t, err, again, "the lexer does not read on after an error")
}
