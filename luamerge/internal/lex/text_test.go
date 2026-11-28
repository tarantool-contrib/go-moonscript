package lex_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

func TestHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		src    string
		header string
	}{
		{name: "empty source", src: "", header: ""},
		{name: "no header", src: "print(1)\n", header: ""},
		{name: "shebang", src: "#!/usr/bin/env lua\nprint(1)\n", header: "#!/usr/bin/env lua"},
		{name: "any line starting with a hash", src: "# comment\nprint(1)\n", header: "# comment"},
		{name: "shebang without a line break", src: "#!/usr/bin/env lua", header: "#!/usr/bin/env lua"},
		{name: "a lone hash", src: "#\nx", header: "#"},
		{name: "CR LF ends at the LF", src: "#!lua\r\nprint(1)\r\n", header: "#!lua\r"},
		{
			// PUC Lua; LuaJIT and Tarantool end the line at the first CR.
			name: "CR line endings end at the first LF", src: "#!lua\rprint(1)\rprint(2)\nprint(3)",
			header: "#!lua\rprint(1)\rprint(2)",
		},
		{name: "byte order mark", src: lex.BOM + "print(1)\n", header: lex.BOM},
		{name: "byte order mark alone", src: lex.BOM, header: lex.BOM},
		{name: "byte order mark and shebang", src: lex.BOM + "#!lua\nprint(1)\n", header: lex.BOM + "#!lua"},
		{name: "hash after white space", src: " #!lua\nprint(1)\n", header: ""},
		{name: "hash on the second line", src: "\n#!lua\nprint(1)\n", header: ""},
		{name: "incomplete byte order mark", src: "\xEF\xBB#!lua\n", header: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.header, tt.src[:lex.Header([]byte(tt.src))])
		})
	}
}

func TestNewFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		src    string
		tokens []tok
	}{
		{
			// The interpreter compiles the skipped line as an empty one.
			name: "shebang keeps the line count",
			src:  "#!/usr/bin/env lua\n\nprint(1)",
			tokens: []tok{
				on(lex.Name, "print", 3), on(lex.Symbol, "(", 3), on(lex.Number, "1", 3), on(lex.Symbol, ")", 3),
			},
		},
		{
			name:   "byte order mark and a CR LF header line",
			src:    lex.BOM + "#!lua\r\nx",
			tokens: []tok{on(lex.Name, "x", 2)},
		},
		{
			name:   "byte order mark only",
			src:    lex.BOM + "x",
			tokens: []tok{on(lex.Name, "x", 1)},
		},
		{
			// As a chunk, '#' is the length operator and '!' starts no token.
			name:   "no header",
			src:    "x #!y",
			tokens: []tok{on(lex.Name, "x", 1), on(lex.Symbol, "#", 1), on(lex.Invalid, "!", 1), on(lex.Name, "y", 1)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.tokens, testTokens(t, lex.NewFile([]byte(tt.src)), tt.src))
		})
	}
}

func TestLineBreak(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		at   int
		want int
	}{
		{src: "a\nb", at: 1, want: 1},
		{src: "a\rb", at: 1, want: 1},
		{src: "\r\n", at: 0, want: 2},
		{src: "\n\r", at: 0, want: 2},
		{src: "\n\n", at: 0, want: 1},
		{src: "\r\r", at: 0, want: 1},
		{src: "\r\n", at: 1, want: 1},
		{src: "a", at: 0, want: 0},
		{src: "a", at: 1, want: 0},
		{src: "", at: 0, want: 0},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, lex.LineBreak([]byte(tt.src), tt.at), "LineBreak(%q, %d)", tt.src, tt.at)
	}
}

func TestIsSpace(t *testing.T) {
	t.Parallel()

	spaces := map[byte]bool{' ': true, '\t': true, '\n': true, '\v': true, '\f': true, '\r': true}

	for c := range 256 {
		assert.Equal(t, spaces[byte(c)], lex.IsSpace(byte(c)), "IsSpace(%q)", byte(c))
	}
}

func TestNeedsSeparator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		left, right string
		want        bool
	}{
		{left: "local", right: "b", want: true},
		{left: "end", right: "local", want: true},
		{left: "a", right: "1", want: true},
		{left: "1", right: "a", want: true},
		{left: "0x1", right: "p", want: true},
		{left: "1", right: "..", want: true},
		{left: "1", right: ".5", want: true},
		{left: "1e", right: "+", want: true},
		{left: "-", right: "-", want: true},
		{left: "[", right: "[", want: true},
		{left: "[", right: "=", want: true},
		{left: "[", right: "[[s]]", want: true},
		{left: ".", right: ".", want: true},
		{left: ".", right: "..", want: true},
		{left: "..", right: ".", want: true},
		{left: ".", right: "5", want: true},
		{left: "=", right: "=", want: true},
		{left: "~", right: "=", want: true},
		{left: "<", right: "=", want: true},
		{left: "<", right: "<", want: true},
		{left: ">", right: "=", want: true},
		{left: ">", right: ">", want: true},
		{left: "/", right: "/", want: true},
		{left: ":", right: ":", want: true},
		{left: "..", right: "2", want: false},
		{left: "1", right: "-", want: false},
		{left: "1e5", right: "-", want: false},
		{left: "0x1e", right: "-", want: false},
		{left: "-", right: "1", want: false},
		{left: "a", right: "(", want: false},
		{left: ")", right: "a", want: false},
		{left: "a", right: ".", want: false},
		{left: "a", right: `"s"`, want: false},
		{left: `"s"`, right: "a", want: false},
		{left: "a", right: "[[s]]", want: false},
		{left: "[[s]]", right: "]", want: false},
		{left: "[", right: "a", want: false},
		{left: "~", right: "~", want: false},
		{left: "=", right: "{", want: false},
		{left: "", right: "a", want: false},
		{left: "a", right: "", want: false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, lex.NeedsSeparator([]byte(tt.left), []byte(tt.right)),
			"NeedsSeparator(%q, %q)", tt.left, tt.right)
	}
}

// TestNeedsSeparator_MatchesTheLexer: NeedsSeparator says what the lexer
// reads, for tokens of every kind followed by every byte: two tokens need a
// separator when the lexer reads the first with the byte after it as
// another token, or fails to.
func TestNeedsSeparator_MatchesTheLexer(t *testing.T) {
	t.Parallel()

	lefts := []string{
		"+", "-", "*", "/", "//", "%", "^", "#", "&", "~", "|", "<<", ">>", "==", "~=", "<=", ">=", "<", ">",
		"=", "(", ")", "{", "}", "[", "]", "::", ";", ":", ",", ".", "..", "...", "!", "?", "$", "\x00",
		"a", "local", "_x1", "\xd0\xbf", "1", "12", "1e", "1E", "1e5", "1e+5", "0x", "0x1", "0X1p", "0x1e",
		"0x1P-4", ".5", "5.", ".5e", "1LL", "2i", "0b1", "3then", "1e5..x", `"s"`, `'s'`, "[[s]]", "[==[s]==]",
	}

	for _, left := range lefts {
		for next := range 256 {
			joined := append([]byte(left), byte(next))

			tok, err := lex.New(joined).Next()
			want := err != nil || tok.Start != 0 || tok.End != len(left)

			assert.Equal(t, want, lex.NeedsSeparator([]byte(left), []byte{byte(next)}),
				"NeedsSeparator(%q, %q)", left, string(rune(next)))
		}
	}
}

func TestKind_String(t *testing.T) {
	t.Parallel()

	names := map[lex.Kind]string{
		lex.EOF: "EOF", lex.Name: "Name", lex.Number: "Number", lex.String: "String",
		lex.LongString: "LongString", lex.Comment: "Comment", lex.LongComment: "LongComment",
		lex.Symbol: "Symbol", lex.Invalid: "Invalid", lex.Invalid + 1: "Kind(9)",
	}

	for kind, want := range names {
		assert.Equal(t, want, kind.String())
	}
}
