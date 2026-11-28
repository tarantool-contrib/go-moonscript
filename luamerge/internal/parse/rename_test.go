package parse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/lex"
	"github.com/tarantool/go-luamerge/internal/parse"
)

// testStaticCalls returns the static calls of require that Parse finds in
// source, whether the parser or the token scanner reads it.
func testStaticCalls(t *testing.T, source string) []parse.RequireInfo {
	t.Helper()

	found, err := parse.New().Parse([]byte(source), "test.lua")
	require.NoError(t, err, "Parse(%q)", source)

	calls := []parse.RequireInfo{}

	for _, call := range found.Requires {
		if call.Static {
			calls = append(calls, call)
		}
	}

	return calls
}

// testPrefixNames returns source with prefix put in front of the module name
// of every static call of require that Parse finds in it.
func testPrefixNames(t *testing.T, source, prefix string) string {
	t.Helper()

	renamed, err := parse.PrefixNames([]byte(source), testStaticCalls(t, source), prefix)
	require.NoError(t, err, "PrefixNames(%q)", source)

	return string(renamed)
}

// testAssertOnlyLiteralsDiffer asserts that after is before with prefix put
// in front of the value of renamed string literals and nothing else changed:
// the same tokens, comments included, of the same kinds, each on the lines
// it was on, with the same bytes between them; a token that differs is a
// string literal whose value is prefix and the value it had.
func testAssertOnlyLiteralsDiffer(t *testing.T, before, after, prefix string, renamed int) {
	t.Helper()

	old, err := lex.New([]byte(before)).Tokens()
	require.NoError(t, err)
	tokens, err := lex.New([]byte(after)).Tokens()
	require.NoError(t, err)
	require.Len(t, tokens, len(old), "the same number of tokens\nbefore: %q\nafter:  %q", before, after)

	changed, oldEnd, end := 0, 0, 0

	for i, tok := range tokens {
		was := old[i]
		assert.Equal(t, before[oldEnd:was.Start], after[end:tok.Start], "the bytes before token %d", i)
		assert.Equal(t, was.Kind, tok.Kind, "the kind of token %d", i)
		assert.Equal(t, [2]int{was.Line, was.EndLine}, [2]int{tok.Line, tok.EndLine}, "the lines of token %d", i)

		if text, oldText := after[tok.Start:tok.End], before[was.Start:was.End]; text != oldText {
			changed++
			value, ok := parse.LiteralValueForTest([]byte(text))
			oldValue, oldOK := parse.LiteralValueForTest([]byte(oldText))
			assert.True(t, ok && oldOK && value == prefix+oldValue,
				"token %d is a literal renamed from %q to %q: %q to %q", i, oldValue, prefix+oldValue, oldText, text)
		}

		oldEnd, end = was.End, tok.End
	}

	assert.Equal(t, before[oldEnd:], after[end:], "the bytes after the last token")
	assert.Equal(t, renamed, changed, "renamed literals\nbefore: %q\nafter:  %q", before, after)
}

// TestPrefixNames_EveryCallForm: the prefix goes into the literal that names
// the module of each static call of require, in every form of the call the
// parser knows, and nothing else of the source changes: not a string of the
// same text elsewhere, not the call of a local named require, not the name
// of a dynamic call.
func TestPrefixNames_EveryCallForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"parentheses", `local m = require("x")`, `local m = require("p.x")`},
		{"a string", `require "x"`, `require "p.x"`},
		{"single quotes", `require 'x'`, `require 'p.x'`},
		{"long bracket", `require[[x]]`, `require[[p.x]]`},
		{"leveled long bracket over a line break", "require [==[\nx]==]\n", "require [==[\np.x]==]\n"},
		{"leveled long bracket over CRLF", "require [=[\r\nx]=]\r\n", "require [=[\r\np.x]=]\r\n"},
		{"in parentheses", `require(("x"))`, `require(("p.x"))`},
		{"more arguments", `require("x", "x")`, `require("p.x", "x")`},
		{"escapes", `require("\x61x")`, `require("p.\x61x")`},
		{"_G.require", `_G.require("x")`, `_G.require("p.x")`},
		{"_ENV.require", `_ENV.require "x"`, `_ENV.require "p.x"`},
		{"_G[\"require\"]", `_G["require"]("x")`, `_G["require"]("p.x")`},
		{"pcall", `local ok, m = pcall(require, "x")`, `local ok, m = pcall(require, "p.x")`},
		{
			"xpcall",
			`xpcall(require, function(e) return "x" end, "x")`,
			`xpcall(require, function(e) return "x" end, "p.x")`,
		},
		{"receiver", `local c = require("x"):new("x")`, `local c = require("p.x"):new("x")`},
		{
			"method chain",
			`app:use(require "a"):use(require "b")`,
			`app:use(require "p.a"):use(require "p.b")`,
		},
		{
			"the same name twice on a line",
			`local a, b = require("x"), require("x")`,
			`local a, b = require("p.x"), require("p.x")`,
		},
		{"the name in another string first", `print("x", require("x"))`, `print("x", require("p.x"))`},
		{
			"comments",
			"-- require(\"x\")\nlocal m = require --[[ \"x\" ]] (\"x\") -- \"x\"\n",
			"-- require(\"x\")\nlocal m = require --[[ \"x\" ]] (\"p.x\") -- \"x\"\n",
		},
		{"a dynamic call", `require(name) require("x" .. y)`, `require(name) require("x" .. y)`},
		{
			"a local function named require",
			"local function require(n) return n end print(require(\"x\"), _G.require(\"x\"))\n",
			"local function require(n) return n end print(require(\"x\"), _G.require(\"p.x\"))\n",
		},
		{
			"a parameter named require",
			"local function load(require) return require(\"x\") end\nreturn require(\"x\")\n",
			"local function load(require) return require(\"x\") end\nreturn require(\"p.x\")\n",
		},
		{
			"a file the parser cannot read",
			"local x = = 1\nlocal m = require(\"x\")\n",
			"local x = = 1\nlocal m = require(\"p.x\")\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := testPrefixNames(t, tt.source, "p.")
			assert.Equal(t, tt.want, got)

			renamed := len(testStaticCalls(t, tt.source))
			testAssertOnlyLiteralsDiffer(t, tt.source, got, "p.", renamed)

			// The source read again requires the prefixed names, on the
			// lines it required the names on.
			calls := testStaticCalls(t, tt.source)
			again := testStaticCalls(t, got)
			require.Len(t, again, len(calls))

			for i, call := range calls {
				assert.Equal(t, "p."+call.Name, again[i].Name)
				assert.Equal(t, call.Line, again[i].Line)
			}
		})
	}
}

// TestPrefixNames_PrefixInTheLiteral: the prefix is written the way the
// literal takes it. A short string gets an escape for its own quote, a
// backslash and a control character, and keeps any other byte as it is; a
// long bracket string takes the prefix as it is.
func TestPrefixNames_PrefixInTheLiteral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		prefix string
		want   string
	}{
		{"double quotes", `require("x")`, "a\"b'c\\d\te\x7fя.", `require("a\"b'c\\d\009e\127я.x")`},
		{"single quotes", `require('x')`, "a\"b'c.", `require('a"b\'c.x')`},
		{"a digit after an escape", `require("1x")`, "a\n", `require("a\0101x")`},
		{"long bracket", `require[[x]]`, "a\"b\\c]d.", `require[[a"b\c]d.x]]`},
		{"leveled long bracket", `require[=[x]=]`, "a]]b.", `require[=[a]]b.x]=]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := testPrefixNames(t, tt.source, tt.prefix)
			assert.Equal(t, tt.want, got)
			testAssertOnlyLiteralsDiffer(t, tt.source, got, tt.prefix, 1)
		})
	}
}

// TestPrefixNames_LongBracketThatCannotTakeThePrefix: a long bracket string
// cannot take a prefix that has its closing bracket or a line break, which
// the string would read as "\n" and move the lines after it. Such a literal
// becomes a short string of the prefixed name, after as many line breaks as
// it had, so every token after it stays on its line.
func TestPrefixNames_LongBracketThatCannotTakeThePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		prefix string
		want   string
	}{
		{"closing bracket", "require[[x]] f()\n", "a]]b.", "require\"a]]b.x\" f()\n"},
		{"line break", "require[[x]] f()\n", "a\nb.", "require\"a\\010b.x\" f()\n"},
		{
			"line break in a string over lines",
			"local m = require [[\nx]]\nerror(\"line 3\")\n",
			"a\r\nb.",
			"local m = require \n\"a\\013\\010b.x\"\nerror(\"line 3\")\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := testPrefixNames(t, tt.source, tt.prefix)
			assert.Equal(t, tt.want, got)

			calls := testStaticCalls(t, got)
			require.Len(t, calls, 1)
			assert.Equal(t, tt.prefix+"x", calls[0].Name)

			// Every token after the literal is on the line it was on.
			old, err := lex.New([]byte(tt.source)).Tokens()
			require.NoError(t, err)
			tokens, err := lex.New([]byte(got)).Tokens()
			require.NoError(t, err)
			require.Len(t, tokens, len(old))
			assert.Equal(t, old[len(old)-1].Line, tokens[len(tokens)-1].Line)
		})
	}
}

// TestPrefixNames_NotACallOfTheSource: a call whose literal is not where
// PrefixNames looks, or names another module, is no call Parse found in the
// source, and PrefixNames refuses it rather than write into the source.
func TestPrefixNames_NotACallOfTheSource(t *testing.T) {
	t.Parallel()

	source := []byte(`local m = require("x")`)
	call := testStaticCalls(t, string(source))[0]

	for name, broken := range map[string]func(parse.RequireInfo) parse.RequireInfo{
		"another name": func(c parse.RequireInfo) parse.RequireInfo { c.Name = "y"; return c },
		"no literal":   func(c parse.RequireInfo) parse.RequireInfo { c.Literal = testNoLiteral(); return c },
		"not a literal": func(c parse.RequireInfo) parse.RequireInfo {
			c.Literal = parse.Span{Start: 0, End: 5}
			return c
		},
		"past the end": func(c parse.RequireInfo) parse.RequireInfo {
			c.Literal.End = len(source) + 1
			return c
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := parse.PrefixNames(source, []parse.RequireInfo{broken(call)}, "p.")
			require.ErrorIs(t, err, parse.ErrNotALiteral)
		})
	}

	_, err := parse.PrefixNames(source, []parse.RequireInfo{call, call}, "p.")
	require.ErrorIs(t, err, parse.ErrNotALiteral, "the same literal twice")
}
