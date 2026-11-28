package parse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
)

// TestParse_ModuleNameEscapes: a module name is the value of its string
// literal as Lua 5.2 and later decode it, LuaJIT and Tarantool included:
// "\x61x" is the module ax, not x61x as Lua 5.1 reads it. A name decoded by
// the older rules is a module the program never loads, so the one it does
// load was not embedded.
func TestParse_ModuleNameEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		{
			name:   "hexadecimal escape",
			source: `require("\x61x")`,
			want:   []parse.RequireInfo{testStatic("ax", 1)},
		},
		{
			name:   "hexadecimal escapes in either case",
			source: `require("\x4A\x4a")`,
			want:   []parse.RequireInfo{testStatic("JJ", 1)},
		},
		{
			name:   "unicode escape",
			source: `require("\u{62}ar")`,
			want:   []parse.RequireInfo{testStatic("bar", 1)},
		},
		{
			name:   "unicode escape of two bytes",
			source: `require("\u{44F}")`,
			want:   []parse.RequireInfo{testStatic("я", 1)},
		},
		{
			// Lua encodes a surrogate like any other code point, where Go
			// would put the replacement character.
			name:   "unicode escape of a surrogate",
			source: `require("\u{D800}")`,
			want:   []parse.RequireInfo{testStatic("\xED\xA0\x80", 1)},
		},
		{
			// Lua 5.4 and later take code points up to 2^31 - 1, encoded
			// in up to six bytes.
			name:   "unicode escape beyond Unicode",
			source: `require("\u{7FFFFFFF}")`,
			want:   []parse.RequireInfo{testStatic("\xFD\xBF\xBF\xBF\xBF\xBF", 1)},
		},
		{
			name:   "z escape skipping spaces",
			source: `require("c\z   z")`,
			want:   []parse.RequireInfo{testStatic("cz", 1)},
		},
		{
			name:   "z escape skipping a line break",
			source: "require(\"c\\z\n   z\")\nrequire(\"n\")\n",
			want:   []parse.RequireInfo{testStatic("cz", 1), testStatic("n", 3)},
		},
		{
			name:   "z escape skipping every kind of white space",
			source: "require(\"c\\z \t\f\v\r\n\n\r z\")\nrequire(\"n\")\n",
			want:   []parse.RequireInfo{testStatic("cz", 1), testStatic("n", 4)},
		},
		{
			name:   "decimal escapes",
			source: `require("\97\098\0991")`,
			want:   []parse.RequireInfo{testStatic("abc1", 1)},
		},
		{
			name:   "single-character escapes",
			source: `require("\a\b\f\n\r\t\v\\\"\'")`,
			want:   []parse.RequireInfo{testStatic("\a\b\f\n\r\t\v\\\"'", 1)},
		},
		{
			name:   "escaped line break",
			source: "require(\"a\\\nb\")\nrequire(\"n\")\n",
			want:   []parse.RequireInfo{testStatic("a\nb", 1), testStatic("n", 3)},
		},
		{
			name:   "escaped CR LF line break",
			source: "require(\"a\\\r\nb\")\r\nrequire(\"n\")\r\n",
			want:   []parse.RequireInfo{testStatic("a\nb", 1), testStatic("n", 3)},
		},
		{
			name:   "long string line breaks read as LF",
			source: "require([[a\r\nb\n\rc\rd]])\nrequire(\"n\")\n",
			want:   []parse.RequireInfo{testStatic("a\nb\nc\nd", 1), testStatic("n", 5)},
		},
		{
			name:   "long string without its first line break",
			source: "require [==[\r\nx]==]\nrequire(\"n\")\n",
			want:   []parse.RequireInfo{testStatic("x", 1), testStatic("n", 3)},
		},
		{
			name:   "bytes beyond ASCII",
			source: "require(\"\xD1\x8F\xFF\")",
			want:   []parse.RequireInfo{testStatic("\xD1\x8F\xFF", 1)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testParse(t, tt.source))
		})
	}
}

// TestParse_WhiteSpace: a form feed and a vertical tab are white space in
// every dialect, and "\r", "\r\n" and "\n\r" each end one line.
func TestParse_WhiteSpace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		{
			name:   "form feed and vertical tab",
			source: "local a = 1\f\vrequire(\"x\")\f\n\vrequire(\"y\")\n",
			want:   []parse.RequireInfo{testStatic("x", 1), testStatic("y", 2)},
		},
		{
			name:   "every line break",
			source: "require(\"a\")\rrequire(\"b\")\r\nrequire(\"c\")\n\rrequire(\"d\")\n",
			want: []parse.RequireInfo{
				testStatic("a", 1), testStatic("b", 2), testStatic("c", 3), testStatic("d", 4),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testParse(t, tt.source))
		})
	}
}
