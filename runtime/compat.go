package runtime

// Lua 5.1 (PUC) compatibility patches for gopher-lua, installed by
// OpenCompat. They make the parts of the base, package and debug
// libraries that MoonScript tooling depends on behave like the reference
// interpreter:
//
//   - chunk names: gopher-lua prints a chunk name verbatim in error
//     positions ("@foo.moon:3: boom"); loadstring, load, loadfile, dofile and
//     the package loaders here convert it like luaO_chunkid ("foo.moon:3:",
//     "=(command line)" -> "(command line)", strings -> [string "..."]), and
//     debug.getinfo reports the original name as source (short_src is
//     the converted one).
//     This is what moonscript.errors relies on: it looks the file name from
//     an error position up as line_tables["@" .. name].
//   - Lua syntax errors from loadstring & co are reported as
//     "<chunkid>:<line>: <message> near '<token>'".
//   - require follows ll_require: package.loaders is read from the package
//     table on every call (so moonscript.insert_loader works), and the
//     "module 'x' not found:" message lists each loader's reason.
//   - debug.traceback labels frames like PUC Lua: native (Go) functions
//     "[C]" instead of "[G]", "(tail call)" instead of "(tailcall)", chunk
//     functions "main chunk" instead of "function <file:0>".
//   - The wrappers of tostring, string.format, debug.getinfo and
//     debug.traceback run the function they wrap in their own frame, so
//     debug hooks see one call, as in Lua 5.1.
//   - string.format's %q escapes like Lua 5.1's addquoted (a newline
//     becomes backslash-newline, "\r" "\\r", NUL "\\000").
//   - tostring and print format numbers with "%.14g" (LUAI_NUMFFORMAT)
//     instead of Go's shortest representation, and print calls the global
//     tostring.

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	lua "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua"
	luaparse "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
)

const luaIDSize = 60 // LUA_IDSIZE

// ChunkID is luaO_chunkid: the name Lua 5.1 shows in error positions and
// tracebacks for a chunk named source.
func ChunkID(source string) string {
	switch {
	case strings.HasPrefix(source, "="):
		s := source[1:]
		if len(s) > luaIDSize-1 {
			s = s[:luaIDSize-1]
		}
		return s
	case strings.HasPrefix(source, "@"):
		s := source[1:]
		max := luaIDSize - len(" '...' ") - 1
		if len(s) > max {
			return "..." + s[len(s)-max:]
		}
		return s
	default:
		max := luaIDSize - len(` [string "..."] `) - 1
		l := strings.IndexAny(source, "\n\r")
		if l < 0 {
			l = len(source)
		}
		if l > max {
			l = max
		}
		if l < len(source) {
			return `[string "` + source[:l] + `..."]`
		}
		return `[string "` + source + `"]`
	}
}

const sourcesKey = "go-moonscript.sources"

// load compiles Lua code as chunk chunkname (a Lua chunk name: "@file",
// "=name" or the source itself) and pushes the function, or pushes nil and
// the error message; it returns the number of values pushed.
func load(L *lua.LState, code, chunkname string) int {
	fn, err := loadLua(L, code, chunkname)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(fn)
	return 1
}

// loadLua compiles code, naming the chunk like PUC Lua (see ChunkID).
func loadLua(L *lua.LState, code, chunkname string) (*lua.LFunction, error) {
	id := ChunkID(chunkname)
	fn, err := L.Load(strings.NewReader(code), id)
	if err != nil {
		return nil, errors.New(syntaxError(err, id, code))
	}
	if !lua.IsBinaryChunk(code) { // a binary chunk keeps its functions' names
		recordSource(L, id, chunkname)
	}
	return fn, nil
}

// recordSource remembers the chunk name behind a chunk id for debug.getinfo,
// unless the id gives it back (sourceOf): the table only grows, and
// loadstring in a loop must not make it grow.
func recordSource(L *lua.LState, id, chunkname string) {
	if src, ok := sourceOf(id); ok && src == chunkname {
		return
	}
	reg := L.Get(lua.RegistryIndex).(*lua.LTable)
	tbl, ok := reg.RawGetString(sourcesKey).(*lua.LTable)
	if !ok {
		tbl = L.NewTable()
		reg.RawSetString(sourcesKey, tbl)
	}
	tbl.RawSetString(id, lua.LString(chunkname))
}

// sourceOf returns the chunk name of a chunk id [string "<source>"] whose
// source was not shortened; ok is false for other ids.
func sourceOf(id string) (src string, ok bool) {
	const prefix, suffix = `[string "`, `"]`
	if strings.HasPrefix(id, prefix) && strings.HasSuffix(id, suffix) && len(id) >= len(prefix)+len(suffix) {
		src = id[len(prefix) : len(id)-len(suffix)]
		if ChunkID(src) == id {
			return src, true
		}
	}
	return "", false
}

// syntaxError formats a gopher-lua parse or compile error the way luaX
// reports errors: "<chunkid>:<line>: <message> near '<token>'".
func syntaxError(err error, id, code string) string {
	var ae *lua.ApiError
	if errors.As(err, &ae) && ae.Cause != nil {
		err = ae.Cause
	}
	var pe *luaparse.Error
	if errors.As(err, &pe) {
		msg := pe.Message // the parser's own: lparser.c's or lj_parse.c's
		if pe.Pos.Line == luaparse.NoLine {
			return msg // an error without a position
		}
		if pe.Pos.Line == luaparse.EOF {
			return fmt.Sprintf("%s:%d: %s near '<eof>'", id, strings.Count(code, "\n")+1, msg)
		}
		return fmt.Sprintf("%s:%d: %s near '%s'", id, pe.Pos.Line, msg, pe.Token)
	}
	var ce *lua.CompileError
	if errors.As(err, &ce) {
		return fmt.Sprintf("%s:%d: %s", id, ce.Line, ce.Message)
	}
	if errors.As(err, &ae) && ae.Object != nil {
		return ae.Object.String()
	}
	return err.Error()
}

// strerror is the C library text for an OS error ("No such file or
// directory"); Go's texts are the same, uncapitalized.
func strerror(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return capitalize(err.Error())
}

// readChunkFile is luaL_loadfile's reading part: the file text with a first
// line starting with '#' blanked (its newline kept, so lines don't shift;
// removed before a binary chunk). Under LuaJIT the text is the file's: the
// parser skips the line itself and refuses bytecode after it.
func readChunkFile(L *lua.LState, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		what := "open"
		var pe *os.PathError
		if errors.As(err, &pe) && pe.Op == "read" {
			what = "read"
		}
		return "", fmt.Errorf("cannot %s %s: %s", what, path, strerror(err))
	}
	text := string(data)
	if strings.HasPrefix(text, "#") && L.Dialect() != lua.DialectLuaJIT {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i:]
		} else {
			text = ""
		}
		if lua.IsBinaryChunk(strings.TrimPrefix(text, "\n")) {
			text = text[1:] // a binary chunk (string.dump) after the line
		}
	}
	return text, nil
}

// loadFile is luaL_loadfile: path "" reads stdin.
func loadFile(L *lua.LState, path string) (*lua.LFunction, error) {
	if path == "" {
		data, err := io.ReadAll(L.Stdin())
		if err != nil {
			return nil, fmt.Errorf("cannot read stdin: %s", strerror(err))
		}
		return loadLua(L, string(data), "=stdin")
	}
	text, err := readChunkFile(L, path)
	if err != nil {
		return nil, err
	}
	return loadLua(L, text, "@"+path)
}

// OpenCompat installs the Lua 5.1 compatibility patches described at the
// top of this file. It needs the base, package, io and debug libraries.
func OpenCompat(L *lua.LState) {
	g := L.G.Global

	g.RawSetString("loadstring", L.NewFunction(func(L *lua.LState) int {
		code := L.CheckString(1)
		return load(L, code, L.OptString(2, code))
	}))

	g.RawSetString("load", L.NewFunction(func(L *lua.LState) int {
		fn := L.CheckFunction(1)
		chunkname := L.OptString(2, "=(load)")
		var buf strings.Builder
		for {
			L.Push(fn)
			L.Call(0, 1)
			piece := L.Get(-1)
			L.Pop(1)
			if piece == lua.LNil {
				break
			}
			s, ok := piece.(lua.LString)
			if !ok {
				if n, isNum := piece.(lua.LNumber); isNum {
					s = lua.LString(numberString(L, float64(n)))
				} else {
					L.Push(lua.LNil)
					L.Push(lua.LString("reader function must return a string"))
					return 2
				}
			}
			if s == "" {
				break
			}
			buf.WriteString(string(s))
		}
		return load(L, buf.String(), chunkname)
	}))

	g.RawSetString("loadfile", L.NewFunction(func(L *lua.LState) int {
		fn, err := loadFile(L, L.OptString(1, ""))
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(fn)
		return 1
	}))

	g.RawSetString("dofile", L.NewFunction(func(L *lua.LState) int {
		fn, err := loadFile(L, L.OptString(1, ""))
		if err != nil {
			L.Error(lua.LString(err.Error()), 0)
		}
		top := L.GetTop()
		L.Push(fn)
		L.Call(0, lua.MultRet)
		return L.GetTop() - top
	}))
	if L.Dialect() == lua.DialectLuaJIT {
		openLuaJITCompat(L) // LuaJIT's load, loadstring and loadfile (luajit.go)
	}

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

	openPackageCompat(L)

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
		if getinfo, ok := dbg.RawGetString("getinfo").(*lua.LFunction); ok {
			dbg.RawSetString("getinfo", L.NewFunction(func(L *lua.LState) int {
				// called in this frame, so that levels count from our caller
				// and no call is added for debug hooks to see
				getinfo.GFunction(L)
				info, ok := L.Get(-1).(*lua.LTable)
				if !ok {
					return 1
				}
				// the chunk names of Lua functions are ChunkIDs: short_src
				// is the id, source the chunk name behind it
				if info.RawGetString("source") == lua.LString("=(tail call)") || info.RawGetString("what") == lua.LString("C") {
					return 1
				}
				if src, ok := info.RawGetString("source").(lua.LString); ok {
					info.RawSetString("short_src", src)
					reg := L.Get(lua.RegistryIndex).(*lua.LTable)
					if tbl, ok := reg.RawGetString(sourcesKey).(*lua.LTable); ok {
						if orig, ok := tbl.RawGetString(string(src)).(lua.LString); ok {
							info.RawSetString("source", orig)
							return 1
						}
					}
					if orig, ok := sourceOf(string(src)); ok {
						info.RawSetString("source", lua.LString(orig))
					}
				}
				return 1
			}))
		}
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
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

// sentinel marks a module being loaded (ll_require's sentinel).
var sentinel = &lua.LUserData{}

func openPackageCompat(L *lua.LState) {
	pkg, ok := L.G.Global.RawGetString("package").(*lua.LTable)
	if !ok {
		return
	}

	loaders := L.NewTable()
	loaders.Append(L.NewFunction(loaderPreload))
	loaders.Append(L.NewFunction(loaderLua))
	pkg.RawSetString("loaders", loaders)

	require := L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		L.SetTop(1)
		loaded, ok := L.GetField(L.Get(lua.RegistryIndex), "_LOADED").(*lua.LTable)
		if !ok {
			raise(L, "'package.loaded' must be a table")
		}
		if v := loaded.RawGetString(name); lua.LVAsBool(v) {
			if v == sentinel {
				raise(L, "loop or previous error loading module '%s'", name)
			}
			L.Push(v)
			return 1
		}
		ls, ok := pkg.RawGetString("loaders").(*lua.LTable)
		if !ok {
			raise(L, "'package.loaders' must be a table")
		}
		var msg strings.Builder
		var fn lua.LValue
		for i := 1; ; i++ {
			loader := ls.RawGetInt(i)
			if loader == lua.LNil {
				raise(L, "module '%s' not found:%s", name, msg.String())
			}
			L.Push(loader)
			L.Push(lua.LString(name))
			L.Call(1, 1)
			res := L.Get(-1)
			L.Pop(1)
			if _, isFn := res.(*lua.LFunction); isFn {
				fn = res
				break
			}
			if s, isStr := res.(lua.LString); isStr {
				msg.WriteString(string(s))
			}
		}
		loaded.RawSetString(name, sentinel)
		L.Push(fn)
		L.Push(lua.LString(name))
		L.Call(1, 1)
		if res := L.Get(-1); res != lua.LNil {
			loaded.RawSetString(name, res)
		}
		L.Pop(1)
		if loaded.RawGetString(name) == sentinel {
			loaded.RawSetString(name, lua.LTrue)
		}
		L.Push(loaded.RawGetString(name))
		return 1
	})
	L.G.Global.RawSetString("require", require)
}

func loaderPreload(L *lua.LState) int {
	name := L.CheckString(1)
	pkg, _ := L.G.Global.RawGetString("package").(*lua.LTable)
	var preload *lua.LTable
	if pkg != nil {
		preload, _ = pkg.RawGetString("preload").(*lua.LTable)
	}
	if preload == nil {
		raise(L, "'package.preload' must be a table")
	}
	if v := preload.RawGetString(name); v != lua.LNil {
		L.Push(v)
		return 1
	}
	L.Push(lua.LString(fmt.Sprintf("\n\tno field package.preload['%s']", name)))
	return 1
}

// findFile is findfile: the first readable expansion of package[pname] for
// name, or "" and the "no file" list.
func findFile(L *lua.LState, name, pname string) (string, string) {
	pkg, _ := L.G.Global.RawGetString("package").(*lua.LTable)
	path := lua.LNil
	if pkg != nil {
		path = pkg.RawGetString(pname)
	}
	ps, ok := path.(lua.LString)
	if !ok {
		raise(L, "'package.%s' must be a string", pname)
	}
	name = strings.ReplaceAll(name, ".", "/")
	var msg strings.Builder
	for _, tmpl := range strings.Split(string(ps), ";") {
		if tmpl == "" {
			continue
		}
		fname := strings.ReplaceAll(tmpl, "?", name)
		if f, err := os.Open(fname); err == nil {
			_ = f.Close() // only opened to probe readability, like readable()
			return fname, ""
		}
		fmt.Fprintf(&msg, "\n\tno file '%s'", fname)
	}
	return "", msg.String()
}

func loaderLua(L *lua.LState) int {
	name := L.CheckString(1)
	fname, msg := findFile(L, name, "path")
	if fname == "" {
		L.Push(lua.LString(msg))
		return 1
	}
	fn, err := loadFile(L, fname)
	if err != nil {
		raise(L, "error loading module '%s' from file '%s':\n\t%s", name, fname, err.Error())
	}
	L.Push(fn)
	return 1
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
