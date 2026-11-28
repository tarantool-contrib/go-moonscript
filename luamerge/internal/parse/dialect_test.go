package parse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
)

// TestParse_DialectSyntax: the requires of a file in the syntax of any
// dialect are found on their lines, next to constructs that gopher-lua, a
// parser of Lua 5.1, rejects: the operators of Lua 5.3, the attributes of Lua
// 5.4, the declarations of Lua 5.5, the numerals of LuaJIT, a statement after
// break (Lua 5.2), names beyond ASCII (LuaJIT). The constructs that read
// differently in different dialects are read the way the dialect that accepts
// the file reads them: goto and global are names where they cannot start a
// statement, and a numeral ends where Lua 5.1 (before "..") or Lua 5.2 and
// 5.3 (before a letter) end it.
func TestParse_DialectSyntax(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		// Lua 5.3 operators.
		{
			name:   "floor division",
			source: "local a = require(\"a\") // 2\nlocal b = 7 // require(\"b\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2)},
		},
		{
			name:   "bitwise and, or and exclusive or",
			source: "local v = require(\"a\") & 3 | require(\"b\") ~ 1\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 1)},
		},
		{
			name:   "bitwise not",
			source: "local v = ~require(\"a\")\nlocal w = ~~1 ~ ~2\n",
			want:   []parse.RequireInfo{testStatic("a", 1)},
		},
		{
			name:   "shifts",
			source: "local v = 1 << require(\"a\") >> 2\n",
			want:   []parse.RequireInfo{testStatic("a", 1)},
		},

		// Lua 5.4 and 5.5 attributes.
		{
			name:   "const and close",
			source: "local x <const> = require(\"a\")\nlocal f <close> = nil\nrequire(\"b\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 3)},
		},
		{
			name:   "attribute over two lines",
			source: "local x <\nconst> = require(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 2)},
		},
		{
			name:   "attribute before the names",
			source: "local <const> a, b <close> = require(\"a\"), nil\n",
			want:   []parse.RequireInfo{testStatic("a", 1)},
		},
		{
			name:   "comparison is no attribute",
			source: "local ok = a < b and c > require(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1)},
		},

		// Lua 5.5 global declarations.
		{
			name:   "global declaration",
			source: "global print, require\nlocal a = require(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 2)},
		},
		{
			name:   "global declaration with values",
			source: "global x, y = require(\"a\"), 2\n",
			want:   []parse.RequireInfo{testStatic("a", 1)},
		},
		{
			name:   "global declaration with attributes",
			source: "global <const> x, y <const> = 1, require(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1)},
		},
		{
			name:   "global declaration of every name",
			source: "global *\nglobal <const> *\nrequire(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 3)},
		},
		{
			name:   "global function",
			source: "global function f()\n  return require(\"a\")\nend\n",
			want:   []parse.RequireInfo{testStatic("a", 2)},
		},
		{
			name:   "global declaration after a statement",
			source: "local x = 1 global y\nrequire(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 2)},
		},
		{
			name:   "global declaration after an attribute",
			source: "local x <close>\nglobal y <const>\nglobal z\nrequire(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 4)},
		},
		{
			name: "global as a name",
			source: "local global = require(\"a\")\n" +
				"global = global * 2 + (global < 3 and 1 or 0)\n" +
				"local t = { global = 1 }\n" +
				"t.global = require(\"b\")\n" +
				"function t.global() end\n" +
				"global \"x\"\n",
			want: []parse.RequireInfo{testStatic("a", 1), testStatic("b", 4)},
		},

		// Numerals.
		{
			name:   "hexadecimal floats",
			source: "local v = 0x1p4 + 0x.8p1 + 0xA.8P-2 + require(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1)},
		},
		{
			name:   "LuaJIT integer, binary and imaginary numerals",
			source: "local v = 1ULL + 0x10LL + 12ull + 2i + 1.5i + 0b101 + require(\"a\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1)},
		},
		{
			name:   "Lua 5.1 numeral before a concatenation",
			source: "local s = 1e5..require(\"a\")\nlocal t = 0x10..require(\"b\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2)},
		},
		{
			name:   "Lua 5.2 numeral before a keyword",
			source: "if x == 3then require(\"a\") end\nif x == 0x10then require(\"b\") end\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2)},
		},

		// Names.
		{
			name:   "names beyond ASCII",
			source: "local \xD0\xBF = require(\"a\")\n\xD0\xBF.x = require(\"b\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2)},
		},
		{
			name:   "goto as a name",
			source: "local goto = require(\"a\")\nlocal t = { goto = goto }\nt.goto = require(\"b\")\nprint(goto)\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 3)},
		},
		{
			name:   "goto statement",
			source: "goto skip\nrequire(\"a\")\n::skip::\nrequire(\"b\")\n",
			want:   []parse.RequireInfo{testStatic("a", 2), testStatic("b", 4)},
		},

		// Lua 5.2 statements.
		{
			name:   "statement after break",
			source: "for i = 1, 2 do\n  break\n  require(\"a\")\nend\n",
			want:   []parse.RequireInfo{testStatic("a", 3)},
		},
		{
			name:   "call across a line break",
			source: "local f = function() return print end\nf()\n(require(\"a\"))\n",
			want:   []parse.RequireInfo{testStatic("a", 3)},
		},
		{
			// The copy writes the long string on one line.
			name:   "call across a line break after a long string",
			source: "local f = function() return print end\nf [[\n]](require(\"a\"))\nf \"b\"\n(require(\"b\"))\n",
			want:   []parse.RequireInfo{testStatic("a", 3), testStatic("b", 5)},
		},
		{
			name: "empty statements",
			source: ";local a = require(\"a\");;\n" +
				"do; require(\"b\") end\n" +
				"if a then ; elseif b then;; else ; end\n" +
				"repeat ; until true\n" +
				"local t = { 1; 2; require(\"c\"); }\n",
			want: []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2), testStatic("c", 5)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testParse(t, tt.source))
		})
	}
}

// TestParse_LuaJITExtensions: LuaJIT 2.1 builds of 2026 read syntax of their
// own, which LuaJIT's own jit.* modules use: "!=" and "!", "&&" and "||", the
// conditional expression `c ? a : b`, "??", compound assignments such as
// `x += 1` (and `x ~= 1` for exclusive or), and a continue statement. The
// requires next to them are found on their lines.
func TestParse_LuaJITExtensions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		{
			name:   "not-equal and not",
			source: "if require(\"a\") != nil and !require(\"b\") then end\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 1)},
		},
		{
			name:   "logical operators and nil coalescing",
			source: "local v = require(\"a\") && x || require(\"b\") ?? require(\"c\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 1), testStatic("c", 1)},
		},
		{
			name: "conditional expressions",
			source: "local v = c ? require(\"a\") : require(\"b\")\n" +
				"local w = c ? (t:m()) : t:f(require(\"d\"))\n" +
				"local u = c ? x ? 1 : 2 : require(\"e\")\n" +
				"local f = c ? function() return t:m(require(\"g\")) end : nil\n",
			want: []parse.RequireInfo{
				testStatic("a", 1), testStatic("b", 1), testStatic("d", 2), testStatic("e", 3), testStatic("g", 4),
			},
		},
		{
			name: "compound assignments",
			source: "x += require(\"a\")\ns ..= require(\"b\")\n" +
				"x -= 1 x *= 2 x /= 3 x %= 4 x &= 5 x |= 6 x <<= 1 x >>= 1 t.x += 1 t[1] += 1\n" +
				"x ~= require(\"c\")\n" +
				"if x ~= require(\"d\") then end\n",
			want: []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2), testStatic("c", 4), testStatic("d", 5)},
		},
		{
			name:   "exclusive or assigned at the start of the file",
			source: "x ~= require(\"a\")\nt.x ~= require(\"b\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2)},
		},
		{
			name:   "arithmetic shift",
			source: "local x = (op << 16) ~>> 16 + require(\"a\")\nx ~>>= require(\"b\")\n",
			want:   []parse.RequireInfo{testStatic("a", 1), testStatic("b", 2)},
		},
		{
			name: "continue",
			source: "for i = 1, 2 do\n  if i == 1 then continue end\n  require(\"a\")\nend\n" +
				"::continue::\ngoto continue\nlocal continue = require(\"b\")\nt.continue = continue\n",
			want: []parse.RequireInfo{testStatic("a", 3), testStatic("b", 7)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testParse(t, tt.source))
		})
	}
}
