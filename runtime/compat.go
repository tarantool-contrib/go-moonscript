package runtime

// Lua 5.1 (PUC) compatibility patches for gopher-lua, installed by
// OpenCompat. They make the parts of the base, package and debug
// libraries that MoonScript tooling depends on behave like the reference
// interpreter:
//
//   - chunk names, syntax errors, loadfile/dofile and require are
//     gopher-lua's own (third_party/gopher-lua/chunkname.go and loadlib.go):
//     Lua chunk names ("@foo.moon", "=(command line)", source strings) are
//     shown as luaO_chunkid does ("foo.moon:3:", "(command line)", [string
//     "..."]) and debug.getinfo reports them as source (short_src is the
//     shown name), which moonscript.errors relies on: it looks the file name
//     from an error position up as line_tables["@" .. name]; require reads
//     package.loaders from the package table on every call (so
//     moonscript.insert_loader works). LoadLua is loadstring's loading.
//   - debug.traceback labels frames like PUC Lua: native (Go) functions
//     "[C]" instead of "[G]", "(tail call)" instead of "(tailcall)", chunk
//     functions "main chunk" instead of "function <file:0>".
//   - The wrappers of tostring, string.format and debug.traceback run the
//     function they wrap in their own frame, so debug hooks see one call,
//     as in Lua 5.1.
//   - string.format's %q escapes like Lua 5.1's addquoted (a newline
//     becomes backslash-newline, "\r" "\\r", NUL "\\000").
//   - tostring and print format numbers with "%.14g" (LUAI_NUMFFORMAT)
//     instead of Go's shortest representation, and print calls the global
//     tostring.

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	lua "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua"
)

// ChunkID is luaO_chunkid: the name Lua 5.1 shows in error positions and
// tracebacks for a chunk named source (lua.ChunkID in DialectLua51).
func ChunkID(source string) string {
	return lua.ChunkID(source, lua.DialectLua51)
}

// loadLua compiles code as chunk chunkname (a Lua chunk name: "@file",
// "=name" or the source itself), named like PUC Lua in error messages
// (lua.LState.LoadChunk).
func loadLua(L *lua.LState, code, chunkname string) (*lua.LFunction, error) {
	return L.LoadChunk(strings.NewReader(code), chunkname)
}

// OpenCompat installs the Lua 5.1 compatibility patches described at the
// top of this file. It needs the base, package, io and debug libraries.
func OpenCompat(L *lua.LState) {
	g := L.G.Global

	tostring := g.RawGetString("tostring").(*lua.LFunction)
	g.RawSetString("tostring", L.NewFunction(func(L *lua.LState) int {
		v := L.CheckAny(1)
		if n, ok := v.(lua.LNumber); ok {
			L.Push(lua.LString(numberString(L, float64(n))))
			return 1
		}
		return tostring.GFunction(L) // in this frame: no call for debug hooks
	}))

	g.RawSetString("print", L.NewFunction(func(L *lua.LState) int {
		n := L.GetTop()
		ts := L.GetGlobal("tostring")
		var buf strings.Builder
		for i := 1; i <= n; i++ {
			L.Push(ts)
			L.Push(L.Get(i))
			L.Call(1, 1)
			s, ok := L.Get(-1).(lua.LString)
			if !ok {
				if num, isNum := L.Get(-1).(lua.LNumber); isNum {
					s = lua.LString(numberString(L, float64(num)))
				} else {
					raise(L, "'tostring' must return a string to 'print'")
				}
			}
			L.Pop(1)
			if i > 1 {
				buf.WriteByte('\t')
			}
			buf.WriteString(string(s))
		}
		buf.WriteByte('\n')
		// to the buffered standard output of the io library, as luaB_print
		// writes to stdout (and ignores fputs errors)
		_, _ = io.WriteString(L.Stdout(), buf.String())
		return 0
	}))

	if strlib, ok := g.RawGetString("string").(*lua.LTable); ok {
		if format, ok := strlib.RawGetString("format").(*lua.LFunction); ok {
			strlib.RawSetString("format", L.NewFunction(func(L *lua.LState) int {
				return stringFormat(L, format)
			}))
		}
	}

	if dbg, ok := g.RawGetString("debug").(*lua.LTable); ok {
		if traceback, ok := dbg.RawGetString("traceback").(*lua.LFunction); ok {
			dbg.RawSetString("traceback", L.NewFunction(func(L *lua.LState) int {
				// called in this frame, so that levels count from our caller
				// and no call is added for debug hooks to see
				traceback.GFunction(L)
				if s, ok := L.Get(-1).(lua.LString); ok {
					L.Replace(-1, lua.LString(FormatTraceback(string(s))))
				}
				return 1
			}))
		}
	}
}

// formatNumber is lua_number2str: "%.14g", with C's inf/nan spellings.
func formatNumber(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		if math.Signbit(f) {
			return "-nan"
		}
		return "nan"
	}
	return strconv.FormatFloat(f, 'g', 14, 64)
}

// where is luaL_where: "<chunkid>:<line>: " for the function running at
// level (0 is the running native function, 1 its caller, ...), "" for a
// native function, a level lost to a tail call, or no function.
func where(L *lua.LState, level int) string {
	dbg, ok := L.GetStack(level)
	if !ok || L.IsTailCallLevel(level) { // GetStack answers the bottom frame for the latter
		return ""
	}
	_, err := L.GetInfo("Sl", dbg, lua.LNil)
	if err != nil || dbg.CurrentLine <= 0 || dbg.What == "C" {
		return ""
	}
	return fmt.Sprintf("%s:%d: ", dbg.Source, dbg.CurrentLine)
}

// raise is luaL_error: the message prefixed with where(L, 1).
func raise(L *lua.LState, format string, args ...any) {
	L.Error(lua.LString(where(L, 1)+fmt.Sprintf(format, args...)), 0)
}

// LoadLua compiles Lua code like loadstring(code, chunkname) after
// OpenCompat: the chunk is named ChunkID(chunkname) in error positions and
// tracebacks, and syntax errors read "<chunkid>:<line>: <msg> near '<tok>'".
func LoadLua(L *lua.LState, code, chunkname string) (*lua.LFunction, error) {
	return loadLua(L, code, chunkname)
}

// FormatTraceback relabels the frames of a gopher-lua traceback (the lines
// after "stack traceback:") the way PUC Lua 5.1 prints them: "[G]" native
// frames become "[C]" ("[C]: ?" when unnamed), "(tailcall): ?" becomes
// "(tail call): ?" and "in function <file:0>" (a chunk) "in main chunk".
// debug.traceback applies it after OpenCompat.
func FormatTraceback(tb string) string {
	const header = "stack traceback:\n"
	i := strings.Index(tb, header)
	if i < 0 {
		return tb
	}
	head, body := tb[:i+len(header)], tb[i+len(header):]
	lines := strings.Split(body, "\n")
	for j, line := range lines {
		switch {
		case line == "\t(tailcall): ?":
			line = "\t(tail call): ?"
		case strings.HasPrefix(line, "\t[G]:"):
			rest := strings.TrimPrefix(line, "\t[G]:")
			if rest == " in function (anonymous)" {
				rest = " ?"
			}
			line = "\t[C]:" + rest
		case strings.HasSuffix(line, ":0>"):
			if k := strings.LastIndex(line, " in function <"); k >= 0 {
				line = line[:k] + " in main chunk"
			}
		}
		lines[j] = line
	}
	return head + strings.Join(lines, "\n")
}

// stringFormat is string.format with Lua 5.1's %q; the other conversions
// are gopher-lua's (format), applied one at a time when %q is present.
func stringFormat(L *lua.LState, format *lua.LFunction) int {
	fmtStr := L.CheckString(1)
	top := L.GetTop()
	if !strings.Contains(fmtStr, "%q") || L.Dialect() == lua.DialectLuaJIT { // LuaJIT's %q is gopher-lua's
		return format.GFunction(L) // in this frame: no call for debug hooks
	}
	var out strings.Builder
	arg := 1
	for i := 0; i < len(fmtStr); i++ {
		c := fmtStr[i]
		if c != '%' {
			out.WriteByte(c)
			continue
		}
		if i+1 < len(fmtStr) && fmtStr[i+1] == '%' {
			out.WriteByte('%')
			i++
			continue
		}
		// %[-+ #0]*[0-9]*(.[0-9]*)?<conv>
		j := i + 1
		for j < len(fmtStr) && strings.IndexByte("-+ #0", fmtStr[j]) >= 0 {
			j++
		}
		for j < len(fmtStr) && (fmtStr[j] >= '0' && fmtStr[j] <= '9' || fmtStr[j] == '.') {
			j++
		}
		if j >= len(fmtStr) {
			L.RaiseError("invalid option '%%' to 'format'")
		}
		arg++
		if arg > top {
			L.ArgError(arg, "no value")
		}
		spec := fmtStr[i : j+1]
		if fmtStr[j] == 'q' {
			out.WriteString(addQuoted(L.CheckString(arg)))
		} else {
			L.Push(format)
			L.Push(lua.LString(spec))
			L.Push(L.Get(arg))
			L.Call(2, 1)
			out.WriteString(L.Get(-1).String())
			L.Pop(1)
		}
		i = j
	}
	L.Push(lua.LString(out.String()))
	return 1
}

// addQuoted is lstrlib's addquoted.
func addQuoted(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\', '\n':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\r':
			b.WriteString("\\r")
		case 0:
			b.WriteString("\\000")
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
