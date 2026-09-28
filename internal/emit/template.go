package emit

import (
	"fmt"
	"strings"
	"text/template"
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
//
// In debug mode the loader loads the body as a chunk of its own instead.
// Aliases delegate to the primary name so the body executes exactly once
// regardless of how it's required.
//
// The loader leaves package.loaded to require, which runs it: a module that
// raises while loading is not cached, and a require cycle fails as it does
// natively (Lua 5.1, LuaJIT and Tarantool report the loop; later versions
// recurse until the C stack overflows).
//
// By default loaders go into package.preload, which the built-in preload
// searcher consults before searching package.path — so embedded modules win.
// In fallback mode (.Fallback) loaders go into package.postload and a searcher
// is appended last, so an on-disk copy on package.path takes precedence and the
// embedded module is used only as a fallback.
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
assert((rawget(_G, "loadstring") or load)({{bracket .Source}}, {{q (chunkname .Path)}}))
{{- end}}{{if .Shebang}}{{.Shebang}}
{{end}}-- Amalgamated by luamerge
-- Entry: {{.EntryName}}

{{if .Prefix}}{{.Prefix}}
{{end}}{{if .Fallback}}package.postload = package.postload or {}
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

{{end}}{{range .Modules}}{{$primary := .PrimaryName}}do
{{template "env"}}local __luamerge_loader = {{if $.Debug -}}
function(...) return {{template "load" .Code}}(...) end
{{- else}}{{template "function" .Code}}{{end}}
if package.{{$.RegTable}}[{{q $primary}}] == nil then package.{{$.RegTable}}[{{q $primary}}] = __luamerge_loader end
end
{{range .AliasNames}}if package.{{$.RegTable}}[{{q .}}] == nil then
  package.{{$.RegTable}}[{{q .}}] = function(...) return require({{q $primary}}) end
end
{{end}}{{end}}{{with .Entry}}{{if $.Suffix}}local __luamerge_main = {{if $.Debug -}}
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
// (`[==[ … ]==]`), choosing a level of `=` that does not collide with any
// closing sequence in source. A newline is inserted right after the opening
// bracket; Lua discards that first newline, so the embedded source keeps its
// original line numbering — which is the whole point in debug mode. The
// source is embedded verbatim (no escaping, no reindentation), so multi-line
// string literals inside modules survive intact.
func luaLongBracket(source string) string {
	level := 0
	for strings.Contains(source, "]"+strings.Repeat("=", level)+"]") {
		level++
	}

	eq := strings.Repeat("=", level)

	return "[" + eq + "[\n" + source + "]" + eq + "]"
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
	Prefix    string
	Suffix    string
	Shebang   string
	Debug     bool
	Fallback  bool
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

type moduleData struct {
	PrimaryName string
	AliasNames  []string
	Code        codeData
}
