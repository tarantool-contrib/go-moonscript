package luascope_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
	"github.com/tarantool-contrib/go-moonscript/luascope"
)

// globalNames returns the names of the globals of a resolution.
func globalNames(info *scope.Info) []string {
	var names []string
	for _, g := range info.Globals {
		names = append(names, g.Name)
	}

	return names
}

func TestParse(t *testing.T) {
	t.Parallel()

	src := []byte("local n = 1 -- one\nprint(n, x)\n")

	chunk, info, err := luascope.Parse(src, "@reader.lua", luascope.Options{})
	require.NoError(t, err)

	assert.Equal(t, "@reader.lua", chunk.Name)
	assert.Equal(t, []string{"print", "x"}, globalNames(info))
	assert.Equal(t, chunk, info.Chunk())
	require.Len(t, chunk.Comments, 1)
	assert.Equal(t, " one", chunk.Comments[0].Text)
	assert.Equal(t, ast.Pos{Offset: 24, Line: 2, Column: 6},
		chunk.Pos(info.Global("print").Reads[0].Span().End))
}

func TestParseSyntaxError(t *testing.T) {
	t.Parallel()

	_, _, err := luascope.Parse([]byte("x = 1\nx = 1 +"), "@reader.lua", luascope.Options{})

	var pe *parse.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "reader.lua", pe.Pos.Source)
	assert.Equal(t, 2, pe.Pos.Line)
	assert.Equal(t, "unexpected symbol", pe.Message)
	assert.Equal(t, "<eof>", pe.Token)
	assert.Equal(t, "reader.lua:2: unexpected symbol near '<eof>'", err.Error())
}

// TestParseDialect checks the differences of the dialects: goto, the
// implicit local "arg" of a vararg function and the "#" line of Lua 5.1.
func TestParseDialect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src    string
		jit    string // The globals or the error under LuaJIT.
		lua51  string // The same under Lua 5.1.
		reason string
	}{
		{"goto l ::l::", "", `[string "goto l ::l::"]:1: '=' expected near 'l'`, "goto"},
		{"local goto = 1", "", "", "goto as a name"},
		{"return function(...) return arg end", "arg", "", "arg"},
		{"return arg", "arg", "arg", "no arg in the main chunk"},
		{"#!/bin/lua\nx = 1", "x", `[string "#!/bin/lua..."]:1: unexpected symbol near '#'`, "#"},
		{"x = 0b101", "x", `[string "x = 0b101"]:1: malformed number near '0b101'`, "numerals"},
	}

	for _, tt := range tests {
		for _, d := range []luascope.Dialect{luascope.LuaJIT, luascope.Lua51} {
			want := tt.jit
			if d == luascope.Lua51 {
				want = tt.lua51
			}

			_, info, err := luascope.Parse([]byte(tt.src), tt.src, luascope.Options{Dialect: d})

			got := ""
			if err != nil {
				got = err.Error()
			} else {
				got = strings.Join(globalNames(info), " ")
			}

			assert.Equal(t, want, got, "%s: %v", tt.reason, d)
		}
	}

	_, _, err := luascope.Parse(nil, "=x", luascope.Options{Dialect: 2})
	require.ErrorIs(t, err, luascope.ErrDialect)
	assert.Equal(t, "Dialect(2)", luascope.Dialect(2).String())
	assert.Equal(t, "LuaJIT", luascope.LuaJIT.String())
	assert.Equal(t, "Lua51", luascope.Lua51.String())
}

// TestParseTooDeep checks a chunk deeper than package scope resolves: a
// chain of 100000 fields. LuaJIT accepts it, the compiler of the fork does
// not, and luascope rejects it.
func TestParseTooDeep(t *testing.T) {
	t.Parallel()

	_, _, err := luascope.Parse([]byte("return t"+strings.Repeat(".x", 100000)), "=deep",
		luascope.Options{})

	var se *scope.Error
	require.ErrorAs(t, err, &se)
	assert.Equal(t, "deep: scope: syntax tree too deep", err.Error())

	_, _, err = luascope.Parse([]byte("return t"+strings.Repeat(".x", 90000)), "=deep",
		luascope.Options{})
	require.NoError(t, err)
}

// TestChunkName checks the chunk name in error messages against LuaJIT:
// the expected names are the ones LuaJIT prints for a syntax error, by
// lj_debug_shortname, and for an error of its bytecode loader, by
// bcread_error.
func TestChunkName(t *testing.T) {
	t.Parallel()

	var (
		path59 = strings.Repeat("p", 50) + "/file.lua"
		path60 = strings.Repeat("p", 51) + "/file.lua"
		path70 = strings.Repeat("d", 61) + "/file.lua"
		cut    = func(s string) string { return "..." + s[len(s)-56:] }
		n59    = strings.Repeat("n", 59)
		s45    = strings.Repeat("s", 45)
		s48    = strings.Repeat("s", 48)
		q60    = strings.Repeat("q", 60)
		high   = "\xd0\xbc\xd0\xb8\xd1\x80" // Bytes 128-255 are not control characters.
	)

	tests := []struct {
		chunk    string
		syntax   string // The name in "name:1: unexpected symbol near '<eof>'".
		bytecode string // The name in "name: cannot load bytecode".
	}{
		// A file name.
		{"@file.lua", "file.lua", "file.lua"},
		{"@", "", ""},
		{"@" + path59, path59, path59},
		{"@" + path60, cut(path60), path60},
		{"@" + path70, cut(path70), path70},
		// A name to print as is.
		{"=shown", "shown", "shown"},
		{"=", "", ""},
		{"=" + n59, n59, n59},
		{"=" + n59 + "n", n59, n59 + "n"},
		// A name taken for the source of the chunk.
		{"plain", `[string "plain"]`, "plain"},
		{"", `[string ""]`, ""},
		{"x = 1 +", `[string "x = 1 +"]`, "x = 1 +"},
		{s48, `[string "` + s48 + `"]`, s48},
		{s48 + "s", `[string "` + s45 + `..."]`, s48 + "s"},
		{"first\nsecond", `[string "first..."]`, "first\nsecond"},
		{s48[1:] + "\n", `[string "` + s45 + `..."]`, s48[1:] + "\n"},
		{high, "[string \"" + high + "\"]", high},
		{"\x1bLJ", `[string "..."]`, "(binary)"},
		// LuaJIT takes the name as a C string.
		{"=a\x00b", "a", "a"},
		{"@a\x00b", "a", "a"},
		{"a\x00b", `[string "a"]`, "a"},
		{"@" + q60 + "\x00tail", cut(q60), q60},
	}

	for _, tt := range tests {
		_, _, err := luascope.Parse([]byte("x = 1 +"), tt.chunk, luascope.Options{})
		require.EqualError(t, err, tt.syntax+":1: unexpected symbol near '<eof>'",
			"chunk %q", tt.chunk)

		_, _, err = luascope.Parse([]byte("\x1bLJ"), tt.chunk, luascope.Options{})
		require.EqualError(t, err, tt.bytecode+": cannot load bytecode", "chunk %q", tt.chunk)
		require.ErrorIs(t, err, luascope.ErrBytecode)

		// LuaJIT does not name the chunk when bytecode follows a header.
		_, _, err = luascope.Parse([]byte("#!lua\n\x1bLJ"), tt.chunk, luascope.Options{})
		require.EqualError(t, err, "cannot load malformed bytecode", "chunk %q", tt.chunk)
		require.False(t, errors.Is(err, luascope.ErrBytecode))
	}
}

// TestChunkName51 checks the chunk name in error messages against Lua 5.1:
// the expected names are the ones lua5.1 prints for a syntax error, by
// luaO_chunkid with the buffer of its lexer, and for an error of its
// bytecode loader, by luaU_undump.
func TestChunkName51(t *testing.T) {
	t.Parallel()

	var (
		p72 = strings.Repeat("p", 72)
		p73 = strings.Repeat("p", 73)
		n79 = strings.Repeat("n", 79)
		s63 = strings.Repeat("s", 63)
	)

	tests := []struct {
		chunk    string
		syntax   string
		bytecode string
	}{
		{"@file.lua", "file.lua", "file.lua"},
		{"@", "", ""},
		{"@" + p72, p72, p72},
		{"@" + p73, "..." + p72, p73},
		{"=shown", "shown", "shown"},
		{"=" + n79, n79, n79},
		{"=" + n79 + "n", n79, n79 + "n"},
		{"plain", `[string "plain"]`, "plain"},
		{"", `[string ""]`, ""},
		{s63, `[string "` + s63 + `"]`, s63},
		{s63 + "s", `[string "` + s63 + `..."]`, s63 + "s"},
		{"first\nsecond", `[string "first..."]`, "first\nsecond"},
		{"a\rb", `[string "a..."]`, "a\rb"},
		{"tab\there", "[string \"tab\there\"]", "tab\there"},
		{"\x1bLJ", "[string \"\x1bLJ\"]", "binary string"},
		{"=a\x00b", "a", "a"},
		{"@a\x00b", "a", "a"},
		{"a\x00b", `[string "a"]`, "a"},
	}

	opts := luascope.Options{Dialect: luascope.Lua51}

	for _, tt := range tests {
		_, _, err := luascope.Parse([]byte("x = 1 +"), tt.chunk, opts)
		require.EqualError(t, err, tt.syntax+":1: unexpected symbol near '<eof>'",
			"chunk %q", tt.chunk)

		_, _, err = luascope.Parse([]byte("\x1bLJ"), tt.chunk, opts)
		require.EqualError(t, err, tt.bytecode+": cannot load bytecode", "chunk %q", tt.chunk)
	}
}
