package lua

// go-moonscript patch: Lua chunk names. Lua names a chunk when it loads it
// (lua_load's chunkname): "@" and a file name for a file, "=" and a name
// to show as it is, or the source text itself for a string; error
// messages and tracebacks show the chunk's id (luaO_chunkid: "file.lua",
// "name", `[string "first line..."]`) and debug.getinfo reports the chunk
// name as source and the id as short_src.
//
// gopher-lua has display names instead: LState.Load(reader, name) shows
// name as it is in error messages ("q.lua:1:"), so its callers pass the
// name to show, not a Lua chunk name. Load keeps that meaning: its name is
// the chunk's id, and its chunk name is "=" + name (what luaO_chunkid
// shows as it is). LState.LoadChunk takes a Lua chunk name, which the base
// library's loadstring, load, loadfile and dofile, LState.LoadFile and
// DoFile, and require pass: their chunks have Lua 5.1's (or LuaJIT's)
// source, short_src and error positions. A function prototype keeps both
// (FunctionProto.SourceName, the id; FunctionProto.Source, the chunk
// name); Debug.Source is the id as before and Debug.ChunkName the chunk
// name.

import (
	"io"
	"strings"
)

const (
	luaIDSize = 60 // LUA_IDSIZE
	luaMaxSrc = 80 // Lua 5.1's llex.c MAXSRC: the ids of syntax errors
)

// ChunkID is the name that error messages and tracebacks show for a chunk
// whose Lua chunk name is chunkname, and debug.getinfo's short_src: Lua
// 5.1's luaO_chunkid under DialectLua51, LuaJIT 2.1's lj_debug_shortname
// under DialectLuaJIT (which shorten long names a little differently).
// "@file" gives the file name ("..." and its end if long), "=name" the name
// (at most 59 bytes), anything else `[string "<its first line>"]` ("..."
// after a shortened line).
func ChunkID(chunkname string, dialect Dialect) string {
	return chunkID(chunkname, dialect, luaIDSize)
}

// syntaxChunkID is the id syntax errors show: Lua 5.1's lexer has a buffer
// of 80 bytes for it (MAXSRC), LuaJIT's the usual 60.
func syntaxChunkID(chunkname string, dialect Dialect) string {
	if dialect == DialectLuaJIT {
		return ChunkID(chunkname, dialect)
	}
	return chunkID(chunkname, dialect, luaMaxSrc)
}

// chunkID is ChunkID with an id of at most size-1 bytes under DialectLua51
// (luaO_chunkid's bufflen).
func chunkID(chunkname string, dialect Dialect, size int) string {
	if i := strings.IndexByte(chunkname, 0); i >= 0 {
		if dialect == DialectLuaJIT && strings.HasPrefix(chunkname, "@") {
			return jitFileID(chunkname[1:], chunkname[1:i])
		}
		chunkname = chunkname[:i] // a C string
	}
	switch {
	case strings.HasPrefix(chunkname, "="):
		s := chunkname[1:]
		if len(s) > size-1 {
			s = s[:size-1]
		}
		return s
	case strings.HasPrefix(chunkname, "@"):
		if dialect == DialectLuaJIT {
			return jitFileID(chunkname[1:], chunkname[1:])
		}
		s := chunkname[1:]
		if max := size - len(" '...' ") - 1; len(s) > max {
			return "..." + s[len(s)-max:]
		}
		return s
	case dialect == DialectLuaJIT:
		// up to the first control character, at most 48 bytes, or 45 and
		// "..." when shortened
		l := 0
		for l < len(chunkname) && l < luaIDSize-12 && chunkname[l] >= ' ' {
			l++
		}
		if l < len(chunkname) {
			return `[string "` + chunkname[:min(l, luaIDSize-15)] + `..."]`
		}
		return `[string "` + chunkname + `"]`
	default:
		// up to the first newline, at most 43 bytes, "..." when shortened
		l := strings.IndexAny(chunkname, "\n\r")
		if l < 0 {
			l = len(chunkname)
		}
		l = min(l, size-len(` [string "..."] `)-1)
		if l < len(chunkname) {
			return `[string "` + chunkname[:l] + `..."]`
		}
		return `[string "` + chunkname + `"]`
	}
}

// jitFileID is lj_debug_shortname's "@" case: name is the file name, cname
// the same up to a NUL byte; whether it is shortened depends on name's
// length.
func jitFileID(name, cname string) string {
	if len(name) >= luaIDSize {
		skip := len(name) - (luaIDSize - 4)
		if skip >= len(cname) {
			return "..."
		}
		return "..." + cname[skip:]
	}
	return cname
}

// chunkName is the Lua chunk name of the chunk fp was loaded from (lua_load's
// chunkname, debug.getinfo's source): Source, or "=" + SourceName for a
// prototype that has none (from Compile).
func (fp *FunctionProto) chunkName() string {
	if fp.hasSource || fp.Source != "" {
		return fp.Source
	}
	return "=" + fp.SourceName
}

// setSource sets the id and the chunk name of fp and of the prototypes it
// nests (compiled with the id of syntax errors).
func setSource(fp *FunctionProto, name, source string) {
	fp.SourceName, fp.Source, fp.hasSource = name, source, true
	for _, child := range fp.FunctionPrototypes {
		setSource(child, name, source)
	}
}

// LoadChunk loads a chunk as lua_load does: chunkname is a Lua chunk name
// ("@" + a file name, "=" + a name shown as it is, or the source text of a
// string chunk). Error messages and tracebacks show ChunkID(chunkname) and
// debug.getinfo reports chunkname as source, as in Lua; Load(reader, name)
// shows name as it is (it is LoadChunk with chunk name "=" + name, but
// never shortened).
func (ls *LState) LoadChunk(reader io.Reader, chunkname string) (*LFunction, error) {
	d := ls.Dialect()
	return ls.load(reader, ChunkID(chunkname, d), syntaxChunkID(chunkname, d), chunkname)
}

// whereMessage is msg after luaL_where(L, 1), the position of the caller of
// the running Go function when it is a Lua function: what luaL_error
// raises, as a value.
func (ls *LState) whereMessage(msg string) string {
	if pos := ls.luaWhere(1); pos != "" {
		return pos + " " + msg
	}
	return msg
}
