package luascope

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

// Dialect is the language of a chunk.
type Dialect int

const (
	// LuaJIT is the dialect of Tarantool's LuaJIT, the default: LuaJIT 2.1
	// with the FFI and without LUAJIT_ENABLE_LUA52COMPAT.
	LuaJIT Dialect = iota
	// Lua51 is the dialect of PUC Lua 5.1.
	Lua51
)

// String returns "LuaJIT" or "Lua51".
func (d Dialect) String() string {
	switch d {
	case LuaJIT:
		return "LuaJIT"
	case Lua51:
		return "Lua51"
	default:
		return "Dialect(" + strconv.Itoa(int(d)) + ")"
	}
}

// Options are the options of Parse. The zero Options parse a chunk of
// Tarantool's LuaJIT.
type Options struct {
	// Dialect is the language of the chunk.
	Dialect Dialect
}

// ErrBytecode is the error for a bytecode chunk, which cannot be analyzed.
// The error Parse returns wraps it with the chunk name the way the bytecode
// loader of LuaJIT prints it: "file.lua: cannot load bytecode".
var ErrBytecode = errors.New("cannot load bytecode")

// ErrDialect is the error for an unknown Dialect.
var ErrDialect = errors.New("luascope: unknown dialect")

// Parse parses a chunk held in memory with the checks of the compiler of
// the dialect, and resolves its names: what globals.Analyze and
// allowlist.Checker.Check do before they analyze it. The chunk and its
// resolution can be passed to globals.AnalyzeChunk and
// allowlist.Checker.CheckChunk, and to any other analysis of the trees of
// package ast and the resolutions of package scope.
//
// The chunk name follows the convention of LuaJIT and is printed in error
// messages the way LuaJIT (or Lua 5.1) prints it: "@file.lua" names a file
// and is printed as file.lua, with the start cut off if it is long;
// "=name" is printed as name; any other name is taken for the source of the
// chunk and printed as [string "name"], cut at the end of its first line.
// The chunk's Name is name, as passed.
//
// A syntax error is a *parse.Error, whose Pos.Source is the chunk name as
// printed. A bytecode chunk is rejected with an error wrapping
// ErrBytecode. A tree more than 100000 levels deep, which only a chain of
// as many fields or calls makes, is rejected with an error wrapping the
// *scope.Error of package scope, which does not resolve it: LuaJIT accepts
// such a chain, the compiler of package lua does not.
//
// The chunk keeps src, without a copy, for the lines and columns of its
// offsets (ast.Chunk.Lines): src must not be modified while the chunk is in
// use. The comments of the source are in Chunk.Comments.
func Parse(src []byte, name string, opts Options) (*ast.Chunk, *scope.Info, error) {
	pd, sd := parse.LuaJIT, scope.LuaJIT

	switch opts.Dialect {
	case LuaJIT:
	case Lua51:
		pd, sd = parse.Lua51, scope.Lua51
	default:
		return nil, nil, fmt.Errorf("%w: %d", ErrDialect, int(opts.Dialect))
	}

	if len(src) > 0 && src[0] == bytecodeMark {
		return nil, nil, fmt.Errorf("%s: %w", bytecodeName(name, opts.Dialect), ErrBytecode)
	}

	id := chunkID(name, opts.Dialect)

	chunk, err := parse.ParseChunk(src, id, parse.Options{Dialect: pd, Comments: true})
	if err != nil {
		return nil, nil, err
	}

	chunk.Name = name

	info, err := scope.Resolve(chunk, sd)
	if err != nil {
		// The resolution of a parsed chunk has no problems, unless the
		// tree is deeper than scope resolves.
		return nil, nil, fmt.Errorf("%s: %w", id, err)
	}

	return chunk, info, nil
}

// bytecodeMark is the first byte of a bytecode dump, LUA_SIGNATURE[0].
const bytecodeMark = 0x1b

// idSize is LUA_IDSIZE, the size of the buffer LuaJIT formats a chunk name
// in for an error message, with the terminating NUL; maxSrc is the size of
// the one of the lexer of Lua 5.1 (MAXSRC).
const (
	idSize = 60
	maxSrc = 80
)

// chunkID formats a chunk name for a syntax error: like lj_debug_shortname
// for LuaJIT, like luaO_chunkid with the buffer of the lexer for Lua 5.1.
// The name is taken as a C string, cut at its first NUL byte.
//
// LuaJIT prints "=name" as name, cut to 59 bytes, and "@file" as file, with
// the start cut off to fit in 59 bytes. Any other name is taken for the
// source of the chunk and printed as [string "name"]; a name with a control
// character or longer than 48 bytes is cut, to at most 45 bytes, and ends
// with "...". Lua 5.1 does the same with 79 bytes for "=name", 72 for
// "@file", and cuts a source at its first line break, to at most 63 bytes.
func chunkID(name string, d Dialect) string {
	name = cString(name)

	if d == Lua51 {
		return chunkID51(name)
	}

	if s, ok := strings.CutPrefix(name, "="); ok {
		return s[:min(len(s), idSize-1)]
	}

	if s, ok := strings.CutPrefix(name, "@"); ok {
		if len(s) >= idSize {
			return "..." + s[len(s)-(idSize-4):]
		}

		return s
	}

	n := 0
	for n < idSize-12 && n < len(name) && name[n] >= ' ' {
		n++
	}

	if n == len(name) {
		return `[string "` + name + `"]`
	}

	return `[string "` + name[:min(n, idSize-15)] + `..."]`
}

// chunkID51 is luaO_chunkid with the buffer of MAXSRC bytes.
func chunkID51(name string) string {
	if s, ok := strings.CutPrefix(name, "="); ok {
		return s[:min(len(s), maxSrc-1)]
	}

	if s, ok := strings.CutPrefix(name, "@"); ok {
		if n := maxSrc - len(" '...' ") - 1; len(s) > n {
			return "..." + s[len(s)-n:]
		}

		return s
	}

	n := strings.IndexAny(name, "\n\r")
	if n < 0 {
		n = len(name)
	}

	n = min(n, maxSrc-len(` [string "..."] `)-1)
	if n < len(name) {
		return `[string "` + name[:n] + `..."]`
	}

	return `[string "` + name + `"]`
}

// bytecodeName formats a chunk name for an error of the bytecode loader
// like bcread_error of LuaJIT and luaU_undump of Lua 5.1: the "@" or "=" is
// dropped, and a name that starts with the bytecode signature, as the
// default name of loadstring does for bytecode, is printed as "(binary)",
// "binary string" for Lua 5.1.
func bytecodeName(name string, d Dialect) string {
	name = cString(name)

	switch {
	case strings.HasPrefix(name, "@"), strings.HasPrefix(name, "="):
		return name[1:]
	case len(name) > 0 && name[0] == bytecodeMark && d == Lua51:
		return "binary string"
	case len(name) > 0 && name[0] == bytecodeMark:
		return "(binary)"
	default:
		return name
	}
}

// cString cuts a chunk name at its first NUL byte: Lua takes the name as a
// C string.
func cString(s string) string {
	if i := strings.IndexByte(s, 0); i >= 0 {
		return s[:i]
	}

	return s
}
