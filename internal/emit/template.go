package emit

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/tarantool/go-luamerge/internal/graph"
)

// templateText renders the bundle. Each module becomes a loader function,
// following the lua-amalg pattern:
//
//	do
//	local _ENV = _VERSION ~= "Lua 5.1" and _ENV or nil
//	local __luamerge_loader = function(...)
//	if _VERSION == "Lua 5.1" and not rawget(_G, "jit") then arg = rawget(_G, "arg") end
//	<the module's source, verbatim>
//	end
//	if package.preload["name"] == nil then package.preload["name"] = __luamerge_loader end
//	end
//
// Why each piece is there:
//
//   - The `_ENV` local lives in the enclosing `do` block, not in the function,
//     so that it is the loader's first upvalue, which Lua 5.2's `module`
//     relies on. On Lua 5.1, LuaJIT and Tarantool `_ENV` is an ordinary name,
//     and reading it reads an undeclared global, which strict mode rejects:
//     there the `and` stops before the read and the local is nil.
//   - The body is the whole block of the function and sees no local of the
//     wrapper: `local f = function` brings f into scope only after the
//     function. A module's globals stay globals whatever their names, and
//     every local slot of the function is the module's. (A vararg function
//     keeps one slot for itself on PUC Lua 5.1 and Lua 5.5.)
//   - PUC Lua 5.1 (LUA_COMPAT_VARARG) gives every vararg function an implicit
//     local `arg`, which would hide the global `arg` from the body; the arg
//     line fills that local with the global table. It is an assignment, not
//     a local: on every other interpreter the condition is false and the line
//     touches nothing, so an assignment to arg in a module stays global.
//     rawget, not _G.arg, so that a strict _G without a global arg does not
//     raise. NoArgFix drops the line.
//   - The line before the body is a statement that ends with `end`, so a body
//     whose first statement starts with `(` cannot continue it as a call.
//   - The source is emitted verbatim, not reindented, so multi-line string
//     literals keep their contents.
//   - A loader is registered only where the slot is still empty. Natively a
//     loader the embedding application put in package.preload wins over the
//     file on package.path, and LuaJIT's own table.new lives there too.
//   - The same loader is registered under every name the project requires
//     the module by. Natively each name is a module of its own: require
//     caches the names apart, so the body runs once per name and sees its
//     own name in `...`.
//
// In debug mode the bundle embeds each body as a string and the loader
// loads it as a chunk of its own, named after its file, with
// __luamerge_load, so errors report the original file and line. The chunk
// gets the environment the bundle itself runs in (load's env argument on
// Lua 5.2 and later, setfenv on Lua 5.1, LuaJIT and Tarantool): the one the
// modules of a default-mode bundle inherit as closures of the bundle's
// chunk. Natively require loads a module in the global environment, which
// makes a difference only for a bundle loaded in an environment of its own.
//
// The names --package-prefix generates (aliases) are no source's require
// names: each loads the name it was generated from, so both give the same
// instance. They are registered after every module, so a module of the
// project keeps its name when an alias would have the same one.
//
// The loader leaves package.loaded to require, which runs it: a module that
// raises while loading is not cached, and a require cycle fails as it does
// natively (Lua 5.1, LuaJIT and Tarantool report the loop; later versions
// recurse until the C stack overflows).
//
// The prefix runs first, in a `do` block of its own: its locals are not
// visible to the rest of the bundle, as they are not to modules loaded as
// chunks of their own in debug mode, and its last statement cannot run on
// into the next one.
//
// By default loaders go into package.preload, which the built-in preload
// searcher consults before searching package.path — so embedded modules win.
// In fallback mode loaders go into package.postload and a searcher is
// appended last, so an on-disk copy on package.path takes precedence and the
// embedded module is used only as a fallback.
//
// A name that some runtime has a built-in module of (a module's LateNames)
// goes into package.postload behind the same appended searcher in either
// mode. Natively, where the runtime has that module, require loads it and
// never the file: from package.loaded, from package.preload (LuaJIT's
// table.new) or, on Tarantool, from a searcher of its own that runs after
// package.preload, where a loader of the bundle would win over it. Behind
// the interpreter's own searchers the embedded module is found only where
// the runtime has no module of that name.
//
// The entry is not a module: its code comes last, at the top level of the
// bundle, so it runs as the main chunk, the way the interpreter runs a script.
// Its `...` is the chunk's own (the command-line arguments), its return values
// are the chunk's, the stack above it is a script's, and it is neither looked
// up through the searchers nor cached in package.loaded. In debug mode its
// loaded chunk is called with the bundle's `...` instead.
//
// A suffix must run after the entry, while `return` must be the last
// statement of a block. With a suffix the entry is therefore a function: the
// bundle calls it with its `...`, keeps every result (select("#") counts a
// trailing nil, which a table alone would lose; Lua 5.1 has no table.pack),
// runs the suffix in a block of its own and returns the results. The call
// puts one more level on the stack above the entry than a script has.
//
// The "function" and "load" templates render the code of a module or of the
// entry (a codeData) as a function in default mode and as a loaded chunk in
// debug mode; "env" is the `_ENV` local around a function. Like the rest of
// the bundle's own code, they read no global that may not exist (loadstring
// is gone from Lua 5.2 on), so the bundle works under a strict _G.
const templateText = `{{define "env" -}}
local _ENV = _VERSION ~= "Lua 5.1" and _ENV or nil
{{end}}{{define "function" -}}
function(...)
{{if .ArgFix}}if _VERSION == "Lua 5.1" and not rawget(_G, "jit") then arg = rawget(_G, "arg") end
{{end}}{{indent .Source 0}}end
{{- end}}{{define "load" -}}
__luamerge_load({{bracket .Source}}, {{q (chunkname .Path)}})
{{- end}}{{if .Shebang}}{{.Shebang}}
{{end}}-- Amalgamated by luamerge
-- Entry: {{.EntryName}}

{{if .Prefix}}do
{{.Prefix}}end

{{end}}{{if .Postload}}package.postload = package.postload or {}
do
  local postload = package.postload
  local searchers = package.searchers or package.loaders
  searchers[#searchers+1] = function(mod)
    local loader = postload[mod]
    if loader == nil then
      return "\n\tno field package.postload['" .. mod .. "']"
    end
    return loader
  end
end

{{end}}{{if .Debug}}local __luamerge_load
do
{{template "env"}}__luamerge_load = function(source, chunkname)
  local chunk = assert((rawget(_G, "loadstring") or load)(source, chunkname, "t", _ENV))
  if _ENV == nil then setfenv(chunk, getfenv(1)) end
  return chunk
end
end

{{end}}{{range .Modules}}do
{{if not $.Debug}}{{template "env"}}{{end}}local __luamerge_loader = {{if $.Debug -}}
function(...) return {{template "load" .Code}}(...) end
{{- else}}{{template "function" .Code}}{{end}}
{{range .Names}}if package.{{$.RegTable}}[{{q .}}] == nil then package.{{$.RegTable}}[{{q .}}] = __luamerge_loader end
{{end}}{{range .LateNames}}if package.postload[{{q .}}] == nil then package.postload[{{q .}}] = __luamerge_loader end
{{end}}end
{{end}}{{range .Aliases}}if package.{{$.RegTable}}[{{q .Name}}] == nil then
  package.{{$.RegTable}}[{{q .Name}}] = function(...) return require({{q .Target}}) end
end
{{end}}{{with .Entry}}{{if $.Suffix}}local __luamerge_main = {{if $.Debug -}}
{{template "load" .}}{{else}}(function()
{{template "env"}}return {{template "function" .}}
end)(){{end}}
local __luamerge_n, __luamerge_results = (function(...) return select("#", ...), { ... } end)(__luamerge_main(...))
do
{{$.Suffix}}end
return (table.unpack or unpack)(__luamerge_results, 1, __luamerge_n)
{{else if $.Debug}}return {{template "load" .}}(...)
{{else}}{{indent .Source 0}}{{end}}{{end}}`

// indent prefixes every line of source with `spaces` spaces and normalizes the
// trailing newline. Module bodies are emitted with spaces=0 (verbatim, column 0)
// on purpose: prepending indentation would alter the contents of multi-line
// `[[ ... ]]` string literals inside a module.
func indent(source string, spaces int) string {
	if source == "" {
		return ""
	}

	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimRight(source, "\n"), "\n")

	var out strings.Builder
	for _, line := range lines {
		out.WriteString(prefix)
		out.WriteString(line)
		out.WriteByte('\n')
	}

	return out.String()
}

// quoteLua renders s as a Lua double-quoted string literal, escaping any
// character that would otherwise break the literal (quotes, backslashes,
// newlines, control bytes). Module names come from file paths and are normally
// trivial, but this keeps the emitter robust against unusual names.
func quoteLua(input string) string {
	var builder strings.Builder

	builder.WriteByte('"')

	for i := range len(input) {
		char := input[i]
		switch char {
		case '\\':
			builder.WriteString(`\\`)
		case '"':
			builder.WriteString(`\"`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		default:
			if char < 0x20 || char == 0x7f {
				fmt.Fprintf(&builder, `\%d`, char)
			} else {
				builder.WriteByte(char)
			}
		}
	}

	builder.WriteByte('"')

	return builder.String()
}

// luaLongBracket wraps source in a Lua long-bracket string literal
// (`[==[ … ]==]`) that holds it verbatim: no escaping, no reindentation, so
// multi-line string literals inside modules survive intact, and every line
// keeps its number, which is the whole point in debug mode. (Lua reads each
// line break inside the string as "\n", which moves no line either.)
//
//   - The level of `=` is the lowest whose closing bracket appears nowhere in
//     source, not even at its end: a source ending in "]", or in "]=" at
//     level 1, would form one together with the closing bracket after it.
//     Looking in source and one more "]" finds both.
//   - A line break follows the opening bracket, and Lua discards it, as it
//     discards the first line break of every long string. It is "\n", or
//     "\r\n" before a source that starts with "\r": Lua reads "\n\r" as one
//     line break, which would discard the first line break of the source
//     too.
func luaLongBracket(source string) string {
	text := source + "]"

	level := 0
	for strings.Contains(text, "]"+strings.Repeat("=", level)+"]") {
		level++
	}

	equals := strings.Repeat("=", level)

	lineBreak := "\n"
	if strings.HasPrefix(source, "\r") {
		lineBreak = "\r\n"
	}

	return "[" + equals + "[" + lineBreak + source + "]" + equals + "]"
}

// chunkName renders the load() chunk name for a module path. The leading '@'
// tells Lua to treat the rest as a file name in error messages and tracebacks.
func chunkName(path string) string {
	return "@" + path
}

//nolint:gochecknoglobals // parsed once at init, read-only for the rest of the run
var outputTemplate = template.Must(
	template.New("output").Funcs(template.FuncMap{
		"indent":    indent,
		"q":         quoteLua,
		"bracket":   luaLongBracket,
		"chunkname": chunkName,
	}).Parse(templateText),
)

type templateData struct {
	EntryName string
	Entry     codeData
	Modules   []moduleData
	// Aliases are the names --package-prefix generated, registered after
	// every module.
	Aliases []graph.Alias
	Prefix  string
	Suffix  string
	Shebang string
	Debug   bool
	// Postload sets up package.postload and the searcher, appended after the
	// interpreter's own, that consults it: in fallback mode, and whenever a
	// module has LateNames.
	Postload bool
	// RegTable is the package field loaders are registered in: "preload"
	// normally, "postload" in fallback mode.
	RegTable string
}

// codeData is the code of a module or of the entry.
type codeData struct {
	// Source is the code, transforms applied.
	Source string
	// Path is the file it comes from, the chunk name in debug mode.
	Path string
	// ArgFix adds the arg line in front of the code when it runs as a
	// function.
	ArgFix bool
}

// moduleData is a module: its code and the names it is registered under,
// every one with the same loader.
type moduleData struct {
	// Names are registered in RegTable.
	Names []string
	// LateNames are the names some runtime has a built-in module of,
	// registered in package.postload behind the interpreter's own searchers.
	// In fallback mode they are among Names instead.
	LateNames []string
	Code      codeData
}
