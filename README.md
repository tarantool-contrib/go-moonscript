[![Go Reference][godoc-badge]][godoc-url]
[![Actions Status][actions-badge]][actions-url]
[![Code Coverage][coverage-badge]][coverage-url]
[![Telegram EN][telegram-badge]][telegram-en-url]
[![Telegram RU][telegram-badge]][telegram-ru-url]

# go-luamerge: bundle a multi-file Lua project into one file

### About

<a href="http://tarantool.org">
    <img align="right" src="assets/logo.png" width="250" alt="go-luamerge logo">
</a>

**luamerge** merges a multi-file Lua project into a single self-contained
`.lua` file, preserving exact `require()` semantics — run-once caching,
circular dependencies, and module identity all behave the same as in the
un-bundled project. It ships as both a Go library and a command-line tool.

It is a Go reimplementation of the ideas in [siffiejoe/lua-amalg](https://github.com/siffiejoe/lua-amalg), with static `require()` analysis (no need to *run* your program to discover modules) and first-class transforms, debug tracebacks, and dev-override modes.

### Requirements

- Go 1.26.5 or later (to build the CLI or import the library).
- A Lua interpreter (Lua 5.1 to 5.5, LuaJIT or Tarantool) to *run* the produced
  bundle. It is not required to build one.

## Features

- **Static dependency analysis** — finds `require()` calls by parsing source, no runtime instrumentation.
- **Faithful `require()` semantics** — modules registered in `package.preload`, so `require` itself caches them, runs each once, and reports a failed load or a require cycle as it does natively.
- **Accurate tracebacks** (`--debug`) — runtime errors report the original `file:line`, not an offset into the bundle.
- **Dev overrides** (`--fallback`) — ship a bundle but let an on-disk module take precedence during development.
- **Directly executable output** (`--shebang`) — prepend a shebang and run the bundle as a program.
- **Source transforms** — strip comments or blank lines, or minify.
- **Module-name remapping** — strip/add package prefixes so library modules resolve to a flat file layout.
- **Every Lua dialect** — modules may be written for Lua 5.1 to 5.5, LuaJIT or Tarantool, and the bundle works on each of them (the `_ENV`/`arg` handling is version-aware).

## Installation

```bash
# Latest release
go install github.com/tarantool/go-luamerge/cmd/luamerge@latest

# Docker
docker pull ghcr.io/tarantool/go-luamerge:latest

# From source
git clone https://github.com/tarantool/go-luamerge
cd go-luamerge
go install ./cmd/luamerge
```

The Docker image is built by the release workflow via GoReleaser, which
stages each platform's binary into its own build context before running
`docker buildx build`; `Dockerfile` cannot be built directly with
`docker build .`. To build it locally, use
`goreleaser release --snapshot --clean` (requires a `docker buildx` builder
that supports multi-platform output).

## Quick start

Given:

```
project/
├── main.lua          -- require("lib.utils")
└── lib/
    └── utils.lua
```

```bash
luamerge --entry project/main.lua --output bundle.lua
lua bundle.lua
```

`bundle.lua` contains every reachable module wrapped in a `package.preload` loader, followed by the code of `main.lua`, which runs as the bundle's main chunk.

## How it works

Each module is registered into Lua's native `package.preload` table instead of being inlined at its call site. The entry is not a module: its code comes last and is the bundle's own main chunk.

```lua
-- Amalgamated by luamerge
-- Entry: main

do
local _ENV = _VERSION ~= "Lua 5.1" and _ENV or nil
local __luamerge_loader = function(...)
if _VERSION == "Lua 5.1" and not rawget(_G, "jit") then arg = rawget(_G, "arg") end
-- ... original module source, verbatim ...
end
if package.preload["lib.utils"] == nil then package.preload["lib.utils"] = __luamerge_loader end
end
-- ... entry source, verbatim ...
```

Why each piece is there:

- **`package.preload[name]`** — standard `require()` finds the loader here before searching the filesystem, so the embedded copy is used, and `require` itself manages `package.loaded`: a module is cached once it has loaded, a module that raised while loading is not, and a require cycle fails as it does natively (Lua 5.1, LuaJIT and Tarantool report the loop; later versions run out of C stack).
- **Registered only when the slot is empty** — a loader the embedding application put in `package.preload` before running the program wins over the embedded copy, as it wins over the file on `package.path` natively; so does LuaJIT's built-in `table.new`.
- **Names of runtime built-ins right after the first searcher** — a module required by a name that some runtime has a built-in module of (a pure-Lua `bit.lua` or `utf8.lua`, a `csv.lua` on Tarantool) is not put in `package.preload` under that name. The module wraps the interpreter's first searcher instead: `package.preload` on PUC Lua and LuaJIT, where LuaJIT keeps `table.new`, and on Tarantool the chain of `package.preload`, override modules and its built-in modules, which comes after `package.preload`, where the embedded copy would win. The wrapper asks that searcher first and hands out the embedded module only when it finds nothing. So the runtime's own module wins where there is one, as natively, and everywhere else the embedded module wins over every file on disk, as the modules in `package.preload` do. For any other name the wrapper returns what the first searcher returns, so the message of a module that is not found stays the interpreter's own.
- **The `_ENV` local in the enclosing `do` block** — makes `_ENV` the loader's *first upvalue*, which Lua 5.2's `module`/`_ENV` handling depends on. It lives outside the function on purpose. On Lua 5.1, LuaJIT and Tarantool `_ENV` is an ordinary name, and the `and` keeps the line from reading it as a global, which strict mode would reject.
- **`local __luamerge_loader = function(...)`** — the name comes into scope only after the function, so the module's code sees no local of the wrapper: its globals stay globals whatever their names, and the function's local variable slots are all the module's.
- **The `arg` line** — PUC Lua 5.1 (`LUA_COMPAT_VARARG`) gives every vararg function an implicit local `arg`, which would hide the global `arg` from the module; there the line fills that local with the global table. On every other interpreter the condition is false and the line touches nothing, so an assignment to `arg` in a module stays global. It reads the global with `rawget`, so a strict `_G` without a global `arg` does not make it raise. Disable with `--no-arg-fix`.
- **Module source is emitted verbatim** (no reindentation) so multi-line `[[ ... ]]` string literals keep their exact contents.
- **The entry's code last, at the top level** — it runs the way the interpreter runs a script: its `...` holds the command-line arguments, all of its return values are the bundle's, an "am I the main script?" check sees a script, and it is neither looked up through the searchers nor cached in `package.loaded`. A second bundle, or a second `dofile` of the same bundle, runs its entry again, and a library bundled into `mylib.lua` loads with `require("mylib")`.

A module reachable under several names is registered under each of them with the same loader: natively each name is a module of its own, which runs once per name and sees its own name in `...`, and so it does in the bundle. A module that requires the entry by name gets the entry the same way, as a module of its own, next to the main chunk. The names `--package-prefix` generates are different: each loads the name it was generated from (`return require("utils")`), so both give the same module; they are registered after every module and never take a name from a module of the project.

`--prefix` code runs first, in a `do ... end` block of its own, so its locals are visible to nothing else. With `--suffix` the entry becomes a function: the bundle calls it with its own `...`, keeps every result, runs the suffix in a block of its own and returns the results.

Every file, the entry included, is read the way the interpreter reads a file it loads: a UTF-8 byte order mark is dropped, and a first line that starts with `#`, such as `#!/usr/bin/env lua`, is left empty. Nothing else changes, blank lines at the top included, so every line keeps its number: a warning names the line of its `require`, and a `--debug` traceback the line of the error. The bundle's own first line is set with `--shebang`. Lua 5.1 does not skip a byte order mark, so there such a file fails to load natively, but runs in a bundle.

## CLI flags

| Flag | Description | Default |
|------|-------------|---------|
| `--config` | Config file; a file named here must exist, while the default is read only if present (`--config ''` is the default) | `luamerge.yaml` |
| `--entry` | Entry Lua file **(required)** | – |
| `--output` | Output file, `-` for stdout; `--output ''` leaves it to the configuration | `-` |
| `--root` | Base directory for module resolution; module names are paths relative to it. Must exist | directory of entry file |
| `--path` | Lua path templates, semicolon-separated | `?.lua;?/init.lua` |
| `--search` | Additional search directories, relative to the working directory, not the root (comma-separated or repeated) | – |
| `--skip` | Exclude packages (exact name, or `name.*` for prefix; comma-separated or repeated) | – |
| `--include` | Force-include modules not reached from the entry (exact names; comma-separated or repeated) | – |
| `--strict` | Treat unresolved static requires as errors, except those in `pcall`/`xpcall` | `false` |
| `--debug` | Load modules via `load(src, "@file")` so tracebacks keep the original `file:line` | `false` |
| `--fallback` | Register modules in `package.postload` behind a searcher so on-disk modules win | `false` |
| `--shebang` | Shebang written as the first line of the bundle (e.g. `#!/usr/bin/env lua`) | – |
| `--no-arg-fix` | Omit the `arg` line, which only acts on PUC Lua 5.1 (`LUA_COMPAT_VARARG`): there it restores the global `arg` inside module code | – |
| `--remove-comments` | Remove comments, keeping line numbers (see [Source transforms](#source-transforms)) | `false` |
| `--remove-empty-lines` | Remove lines that hold only white space, outside strings and comments | `false` |
| `--minify` | Remove comments and the white space between tokens (see [Source transforms](#source-transforms)) | `false` |
| `--prefix` | Lua code run before the modules are registered, in a block of its own | – |
| `--suffix` | Lua code run after the entry, in a block of its own | – |
| `--package-prefix` | Add a prefix to all module names in output (e.g. `mypkg` → `require("mypkg.module")`) | – |
| `--strip-prefix` | Strip a prefix from module names during resolution (e.g. `tuple_diff` → file `lib/foo.lua`) | – |
| `--package-name` | Convenience: `--strip-prefix` and `--package-prefix` take this value unless set themselves | – |
| `--version` | Print version and exit: a release's own, or the module version and commit the Go toolchain recorded (`go install`, `go build`) | – |

Boolean flags accept a bare form (`--debug`) or an explicit value after `=` (`--debug=false`). luamerge takes no positional arguments, so a value written apart, as in `--debug false`, is refused rather than read as `--debug` followed by a stray `false`; so is an entry given without `--entry`. Such a mistake exits with status 2, like an unknown flag.

The list flags `--search`, `--skip` and `--include` take comma-separated items and may be repeated: `--skip cjson,socket` is `--skip cjson --skip socket`. Spaces around an item and empty items are dropped. Like every other flag they override the configuration, so a list given on the command line *replaces* the list from `luamerge.yaml` or the environment instead of adding to it: with `skip_packages: [cjson]` in `luamerge.yaml`, `--skip socket` skips only `socket`, `--skip cjson,socket` skips both, and `--skip ''` skips nothing.

The output is written only once the build has succeeded: a failed build leaves an existing bundle untouched and creates no directories, and with `--output -` it prints nothing. A regular file must be writable; it is replaced atomically (the bundle is written to a temporary file in the same directory, which is then renamed over it) and keeps its permission bits, so a failed or interrupted write never leaves a truncated bundle. The replaced file is a new file: a hard link to the old bundle keeps the old contents. A symlink is written through to its target, and a device such as `/dev/null` is written to. An `--output` that is the same file as the entry, a bundled module or the config file is refused.

## Source transforms

The transforms read each module with a lexer that knows the lexical grammar of Lua 5.1 to 5.5, LuaJIT and Tarantool (long brackets of any level, every string escape, the line breaks `\n`, `\r`, `\r\n` and `\n\r`), and they never change a token: strings, numbers and names reach the bundle byte for byte. A module the lexer cannot read, such as one with an unfinished string, fails the build.

- `--remove-comments` removes every comment. A removed comment keeps the line breaks it contained, so every line keeps its number and `--debug` tracebacks stay exact; where the tokens on both sides would otherwise run together (`local--[[c]]b`), a space stays between them.
- `--remove-empty-lines` removes the lines that hold nothing but white space between tokens; a blank line inside a long string or a long comment is part of it and stays. The lines after a removed one move up, so `--debug` line numbers no longer match the source.
- `--minify` removes the comments and the white space between tokens. A single space stays where two tokens would otherwise run together (`local x`, `a - -b`, `1 ..2`), and a single line break where the source had one or more: Lua 5.1 and LuaJIT read a `(` at the start of a line differently from Lua 5.2+, so no line break is taken out or put in. Line numbers are not kept.

## Configuration

Settings are resolved with this precedence (later wins): **defaults → `luamerge.yaml` → `LUAMERGE_*` environment variables → CLI flags**. A later source replaces a setting as a whole, lists included: a list is never merged with the list of an earlier source.

Defaults, the YAML file, and the environment are assembled and merged by the Tarantool ecosystem's [go-config](https://github.com/tarantool/go-config) library, and the merged result is validated against a JSON Schema (draft 2020-12) before use — an unknown key or a wrong-typed value in `luamerge.yaml` is reported as an error rather than being silently ignored. CLI flags are applied on top of the validated config.

### YAML

The file is `luamerge.yaml` in the working directory, or the one named with `--config`. The default file is optional; a file named with `--config` must exist, so a typo in its name is an error rather than a build without its settings. An empty file, or one with only comments, holds no settings: `--config /dev/null` builds from the defaults, the environment and the flags alone, whatever `luamerge.yaml` says.

```yaml
# Relative paths are taken from the working directory, not from this file's.
entry: src/main.lua
output: dist/bundle.lua
root: src/
path: "?.lua;?/init.lua"
search:
  - lib/
  - vendor/

strict: false
debug: false        # load() each module so tracebacks keep original file:line
fallback: false     # prefer on-disk modules over embedded copies
arg_fix: true       # emit the arg line for PUC Lua 5.1 (set false for amalg's -a)
shebang: ""         # e.g. "#!/usr/bin/env lua"

# prefix: |          # Lua code run before the modules are registered
#   print("starting")
# suffix: |          # Lua code run after the entry
#   print("done")

# package_name: "mypkg"     # convenience: strip + re-add the prefix
# strip_prefix: "tuple_diff"
# package_prefix: "mypkg"

# skip_packages:            # exclude from the bundle (left as runtime requires)
#   - "cjson"
#   - "xlog.*"
# include_packages:         # bundle even if not reached from entry
#   - "plugin.optional"

transform:
  remove_comments: false
  remove_empty_lines: false
  minify: false
```

### Environment variables

Every setting of `luamerge.yaml` can also be given in the environment (handy in CI). The variable is `LUAMERGE_` followed by the setting's **YAML key** upper-cased, a nested key joined with `_`; it is not named after the flag:

```bash
LUAMERGE_ENTRY=src/main.lua LUAMERGE_OUTPUT=dist/bundle.lua LUAMERGE_STRICT=true luamerge
```

Where the flag's name differs from the key:

| Flag | YAML key | Environment variable |
|------|----------|----------------------|
| `--skip` | `skip_packages` | `LUAMERGE_SKIP_PACKAGES` |
| `--include` | `include_packages` | `LUAMERGE_INCLUDE_PACKAGES` |
| `--no-arg-fix` | `arg_fix` (the inverse) | `LUAMERGE_ARG_FIX=false` |
| `--remove-comments` | `transform.remove_comments` | `LUAMERGE_TRANSFORM_REMOVE_COMMENTS` |
| `--remove-empty-lines` | `transform.remove_empty_lines` | `LUAMERGE_TRANSFORM_REMOVE_EMPTY_LINES` |
| `--minify` | `transform.minify` | `LUAMERGE_TRANSFORM_MINIFY` |
| `--strip-shebang` | `transform.strip_shebang` | `LUAMERGE_TRANSFORM_STRIP_SHEBANG` |
| `--config`, `--version` | – | – |

Every other variable is named after its flag as well as its key: `LUAMERGE_ENTRY`, `LUAMERGE_OUTPUT`, `LUAMERGE_ROOT`, `LUAMERGE_PATH`, `LUAMERGE_SEARCH`, `LUAMERGE_STRICT`, `LUAMERGE_DEBUG`, `LUAMERGE_FALLBACK`, `LUAMERGE_SHEBANG`, `LUAMERGE_PREFIX`, `LUAMERGE_SUFFIX`, `LUAMERGE_PACKAGE_PREFIX`, `LUAMERGE_PACKAGE_NAME`, `LUAMERGE_STRIP_PREFIX`. A `LUAMERGE_*` variable that names no setting, such as `LUAMERGE_MINIFY` or `LUAMERGE_SKIP`, is an error rather than being ignored.

A boolean takes `true` or `false` (or `1`, `0`). A list is comma-separated, as on the command line: `LUAMERGE_SEARCH=lib,vendor`, `LUAMERGE_SKIP_PACKAGES='cjson,xlog.*'`. Spaces around an item and empty items are dropped, so an empty value is the empty list and clears a list set in `luamerge.yaml`. A list in `luamerge.yaml` is a YAML list and is never split: a directory whose name contains a comma can only be listed there.

### Paths

A relative path is taken from the working directory luamerge runs in, wherever it is given: a flag, a `LUAMERGE_*` variable or the config file. It is **not** taken from the config file's directory: `--config build/luamerge.yaml` with `entry: src/main.lua` bundles `./src/main.lua`, not `./build/src/main.lua`. The same holds for `root`, `output` and every `search` directory; in particular a relative `--search lib` (or `search: [lib]`) is `./lib`, not a directory under the root.

The entry and the root are made absolute and their symbolic links resolved before any module name is computed, since a module's name is its path relative to the root: `--root src`, `--root "$PWD/src"` and a root reached through a symbolic link name the modules alike. Both must exist.

At runtime the bundle looks nothing up for the modules it embeds. What it leaves to `require` (skipped, unresolved and dynamic requires, C modules, and with `--fallback` any on-disk copy meant to win) is found through `package.path` and `package.cpath`, whose `./?.lua`-style templates are relative to the program's working directory. Those lookups assume that the program runs from the root, where the templates resolve a module name to the file luamerge would have taken.

## Examples

### Bundle to stdout and pipe straight into Lua

```bash
luamerge --entry src/main.lua --output - | lua -
```

### A directly executable program

```bash
luamerge --entry src/main.lua --shebang '#!/usr/bin/env lua' --output app
chmod +x app
./app
```

### Development build with accurate tracebacks

```bash
luamerge --entry src/main.lua --debug --output bundle.lua
```

A runtime error now points at the real source. Compare:

```
# without --debug
lua: bundle.lua:142: attempt to call a nil value

# with --debug
lua: src/lib/parser.lua:17: attempt to call a nil value
```

In debug mode each module, and the entry, is embedded as a string and run through `load(src, "@path")` in the environment the bundle runs in, so the VM keeps the original chunk name and line numbers. Every line keeps its number, after a shebang line or blank lines at the top of a file too, and so it does with `--remove-comments`. `--remove-empty-lines` and `--minify` move lines: with either of them the numbers no longer match the source. A module is compiled only when it is first required. Keep it **off for releases**: it adds a runtime dependency on `load`/`loadstring` and defeats `luac` precompilation.

### Ship a bundle but override a module locally

```bash
luamerge --entry src/main.lua --fallback --output bundle.lua

# Run with a patched module on the path — the on-disk copy wins:
LUA_PATH="./patches/?.lua;;" lua bundle.lua
```

In fallback mode loaders live in `package.postload` behind a searcher appended *after* the path searchers, so anything found on `package.path` takes precedence. Without `--fallback`, the embedded copy always wins.

### Minified release build

```bash
luamerge --entry src/main.lua --minify --output dist/bundle.lua
```

`--minify` subsumes comment and empty-line removal, and keeps every token as it is (see [Source transforms](#source-transforms)).

### Inject startup / teardown code

```bash
luamerge --entry src/main.lua \
  --prefix 'require("strict").on()' \
  --suffix 'print("bundle loaded")' \
  --output bundle.lua
```

The prefix runs before any module is registered, in a block of its own, and the bundle's own code reads no undeclared global, so it works under strict mode (Tarantool's `strict` here). The suffix runs after the entry; the bundle still returns whatever the entry returns.

### Package a library so it resolves to a flat file layout

Modules are named `mypkg.*` but files live directly under `src/` (no `mypkg/` directory):

```bash
luamerge --entry src/init.lua --package-name mypkg --output mypkg.lua
```

`--package-name mypkg` strips `mypkg.` while resolving (`require("mypkg.utils")` → `src/utils.lua`) and re-adds it to the output names, so the bundle is usable as `require("mypkg.utils")`.

### Exclude external dependencies

Leave system/3rd-party modules as ordinary runtime requires instead of embedding them:

```bash
luamerge --entry src/main.lua \
  --skip cjson --skip socket --skip 'xlog.*' \
  --output bundle.lua
```

### Fail the build on a missing module (CI)

```bash
luamerge --entry src/main.lua --strict --output /dev/null
```

With `--strict`, an unresolved static `require()` is an error (non-zero exit) instead of a warning. A `require` called in protected mode — `pcall(require, "cjson")` or `xpcall(require, handler, "cjson")`, the idiom of an optional dependency — stays a warning: the program is written to run on without that module, and the bundle leaves the require to runtime. Only these literal forms count; a `require` inside a function that `pcall` calls does not.

### Search additional directories

```bash
luamerge --entry src/main.lua --search lib --search vendor --output bundle.lua
```

### Use a config file, override one value on the CLI

```bash
luamerge --config build/luamerge.yaml --output /tmp/bundle.lua
```

## Use as a Go library

The bundler is exposed as a public package for embedding in other tools; the CLI
is a thin wrapper over it. Import the module root (package `luamerge`):

```go
import (
	"bytes"
	"fmt"

	"github.com/tarantool/go-luamerge"
)

func build() error {
	opts := luamerge.DefaultOptions() // sensible defaults (arg-fix on, etc.)
	opts.Entry = "src/main.lua"
	opts.Debug = true // any option is a plain struct field

	var buf bytes.Buffer
	res, err := luamerge.Bundle(opts, &buf)
	if err != nil {
		return err
	}
	for _, w := range res.Warnings {
		if w.File == "" { // About a module of Options.IncludePackages.
			fmt.Printf("warning: %s\n", w.Message)
			continue
		}
		// The message does not repeat the position.
		fmt.Printf("warning: %s:%d: %s\n", w.File, w.Line, w.Message)
	}
	// buf now holds the bundle.
	return nil
}
```

Public API:

- `luamerge.Options` — all bundling settings (entry, root, path, transforms, `Debug`, `Fallback`, `Shebang`, `ArgFix`, prefixes, skip/include, …). `Bundle` writes to the `io.Writer` you give it and ignores `Options.Output`.
- `luamerge.DefaultOptions() Options` — defaults matching the CLI.
- `luamerge.LoadOptions(path string) (Options, error)` — load from a YAML file plus `LUAMERGE_*` env vars. `""` means `luamerge.yaml` in the working directory, which may be absent; a file named explicitly must exist (a missing one is an error matching `fs.ErrNotExist`). The options come back as given, with the `package_name` convenience left to `Bundle`, so any field can still be changed.
- `luamerge.Bundle(opts Options, w io.Writer) (*Result, error)` — runs the full pipeline; resolves the root and the `package_name` convenience itself.
- `luamerge.Result{ Warnings []Warning; Files []string }` — non-fatal issues, and the absolute path of every source file embedded in the bundle (the entry first). Each `Warning` is machine-readable: `Kind` (`WarnDynamicRequire`, `WarnSkipped`, `WarnUnresolved`, `WarnCModule`, `WarnNonLua`, `WarnIndirectRequire`, `WarnShadowsBuiltin`, `WarnTokenScan`), `Module` (the require name), plus `File`/`Line`/`Message` for display.

Errors are classifiable rather than string-matched:

```go
res, err := luamerge.Bundle(opts, w)
switch {
case errors.Is(err, luamerge.ErrNoEntry):
	// Options.Entry was empty
case errors.Is(err, luamerge.ErrModuleNotFound):
	var ue *luamerge.UnresolvedError // strict mode, carries Module/File/Line
	errors.As(err, &ue)
}
```

Implementation packages live under `internal/` and are intentionally not importable; build against the facade above.

## Finding requires

Every file is read with a lexer that knows the syntax of Lua 5.1 to 5.5, LuaJIT and Tarantool, and the [gopher-lua](https://github.com/yuin/gopher-lua) parser, which reads Lua 5.1, gets a copy of it with the newer syntax written the Lua 5.1 way: the operators of Lua 5.3 (`//`, `&`, `|`, `~`, `<<`, `>>`), the attributes of Lua 5.4 and 5.5 (`<const>`, `<close>`), the `global` declarations of Lua 5.5, hexadecimal floats, the numerals of LuaJIT and Tarantool (`1LL`, `0x10ULL`, `2i`, `0b101`), a statement after `break` (Lua 5.2), names beyond ASCII (LuaJIT) and the string escapes Lua 5.1 does not have. Every token of the copy stays on its line, so a warning names the line of the file. The copy is only read to find the requires: the bundle embeds every file as it is.

Where the dialects read the same text differently, it is read the way the dialect that accepts it reads it. A string is decoded as Lua 5.2 and later, LuaJIT and Tarantool decode it: `require("\x61x")` loads the module `ax`, where Lua 5.1 reads an escape it does not know as the character after the backslash, `x61x`. `goto` is a name where it cannot start a `goto` statement (Lua 5.1, LuaJIT), and `global` wherever it does not start a Lua 5.5 declaration. A numeral ends before `..` where Lua 5.1 ends it (`1e5..x`), and before a letter where Lua 5.2 and 5.3 end it (`3then`).

A file the parser cannot read, usually one with a syntax error, does not fail the build: its tokens are scanned for the requires instead, and a `token-scan` warning names the line the parser failed on. The token scanner finds the calls the parser finds, in the same forms, and reports every other use of `require` as an `indirect-require`, but it knows no scopes: a local named `require` is the global one to it. Only a file that no dialect can even split into tokens, such as one with a string or a long comment without its end, fails the build, with the line of the token.

## Module resolution

A `require()` name is resolved the way Lua's own `require` finds a Lua module, with `package.searchpath`, as if the program ran from `root`:

1. Every `.` in the name becomes the directory separator (`foo.bar` → `foo/bar`), in a name written as a path too, as in Lua: `a/b.c` is `a/b/c`, and `./foo`, `../foo` and `.foo` all name `foo`. So a name alone never reaches outside `root` or a search directory.
2. The result replaces the `?` of every template in `--path` (`?.lua`, then `?/init.lua`, …), relative to `root` and then relative to each `--search` directory, in order. For `--search lib` that is the `package.path` `./?.lua;./?/init.lua;lib/?.lua;lib/?/init.lua`. A relative search directory is taken from the working directory luamerge runs in, not from `root` (see [Paths](#paths)); run from the root, as here, the two are the same. A template given as an absolute path (`--path "$PWD/lib/?.lua"`) is tried as is, once, instead of being joined under `root` or a search directory — the same file `LUA_PATH="$PWD/lib/?.lua" lua main.lua` would load. An empty entry in `--path`, from a leading, trailing or doubled `;` (`--path "?.lua;;lib/?.lua"`), stands for the default templates `?.lua;?/init.lua` at that position, the convention `LUA_PATH`'s own `;;` uses for "the default path here".
3. The first readable file wins.

The file that contains the `require()` plays no part, as in Lua: `require("util")` is `root/util.lua` in every module, including one in `sub/` next to a `sub/util.lua`. One module name registered for two different files is an error.

The standard library — `_G`, `coroutine`, `debug`, `io`, `math`, `os`, `package`, `string` and `table` — is never looked up. Every interpreter has it in `package.loaded` before the program starts, so `require("debug")` returns it without searching and never loads a file of that name; the bundler neither embeds nor reports it, even with `--strict`. `--include` of one of these names is skipped with a warning.

Some modules only some runtimes have built in: `bit32` (Lua 5.2 and 5.3), `utf8` (Lua 5.3 and later, and Tarantool), LuaJIT's `bit`, `ffi`, `jit`, `table.new`, `string.buffer` and the like, and Tarantool's own, such as `json`, `fiber` or `csv`. A `require` of one that the project has no file for is left to the runtime and not reported, even with `--strict`: where the runtime has the module it loads it, and elsewhere the program fails as it does natively. A file the project does have for such a name — a pure-Lua `bit.lua` or `utf8.lua` for the runtimes without one — is embedded and served right after the interpreter's first searcher (see [How it works](#how-it-works)), with a `shadows-builtin` warning that names the runtimes which load their own module instead. The list, read from the interpreters themselves, is in [`internal/graph/builtin.go`](internal/graph/builtin.go).

### Prefix stripping

Some projects name their modules with a package prefix that the directory layout does not mirror. A name that the lookup above finds nowhere is tried again without such a prefix. Only then, so a prefix is never stripped from a name that Lua itself would load a file for:

- **`--strip-prefix tuple_diff`** — strips `tuple_diff.`, so `require("tuple_diff.lib.foo")` finds `lib/foo.lua`.
- **Root name** — when `--strip-prefix` does not apply and the root directory's name contains a hyphen, that name is stripped the same way, the hyphen read as an underscore (root `tuple-diff` strips `tuple_diff.`). A root name without a hyphen, such as `myapp`, is never auto-stripped — `require("myapp.util")` stays unresolved unless `--strip-prefix` or `--package-name` says otherwise, exactly as it is natively.
- **Search-directory name** — in a search directory named `lib`, a leading `lib.` is stripped too, so `require("lib.config")` also finds `config.lua` there.
- **`--package-name mypkg`** — strip *and* re-add the prefix, so a library bundles from a flat layout yet stays `require("mypkg.*")`-compatible.

## Output format

Normal (default) — a module required as both `mymod` and `mymod.init` is registered under both names:

```lua
-- Amalgamated by luamerge
-- Entry: main

do
local _ENV = _VERSION ~= "Lua 5.1" and _ENV or nil
local __luamerge_loader = function(...)
if _VERSION == "Lua 5.1" and not rawget(_G, "jit") then arg = rawget(_G, "arg") end
-- module source, verbatim
end
if package.preload["mymod"] == nil then package.preload["mymod"] = __luamerge_loader end
if package.preload["mymod.init"] == nil then package.preload["mymod.init"] = __luamerge_loader end
end
-- entry source, verbatim
```

With `--package-prefix mypkg`, each generated name is registered after every module and loads the name it was generated from:

```lua
if package.preload["mypkg.mymod"] == nil then
  package.preload["mypkg.mymod"] = function(...) return require("mymod") end
end
```

With `--prefix` and `--suffix` — the prefix gets a block of its own, and the entry becomes a function whose results the bundle keeps while the suffix runs:

```lua
do
-- prefix code
end

-- ... modules ...
local __luamerge_main = (function()
local _ENV = _VERSION ~= "Lua 5.1" and _ENV or nil
return function(...)
if _VERSION == "Lua 5.1" and not rawget(_G, "jit") then arg = rawget(_G, "arg") end
-- entry source, verbatim
end
end)()
local __luamerge_n, __luamerge_results = (function(...) return select("#", ...), { ... } end)(__luamerge_main(...))
do
-- suffix code
end
return (table.unpack or unpack)(__luamerge_results, 1, __luamerge_n)
```

Debug (`--debug`) — bodies are embedded as strings and loaded with their file's path (absolute) as the chunk name, in the bundle's environment:

```lua
local __luamerge_load
do
local _ENV = _VERSION ~= "Lua 5.1" and _ENV or nil
__luamerge_load = function(source, chunkname)
  local chunk = assert((rawget(_G, "loadstring") or load)(source, chunkname, "t", _ENV))
  if _ENV == nil then setfenv(chunk, getfenv(1)) end
  return chunk
end
end

do
local __luamerge_loader = function(...) return __luamerge_load([[
-- module source, verbatim
]], "@/path/to/project/src/mymod.lua")(...) end
if package.preload["mymod"] == nil then package.preload["mymod"] = __luamerge_loader end
end
return __luamerge_load([[
-- entry source, verbatim
]], "@/path/to/project/src/main.lua")(...)
```

Fallback (`--fallback`) — a searcher is appended and loaders go into `package.postload`:

```lua
package.postload = package.postload or {}
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
```

A module required by the name of a runtime's built-in module wraps the interpreter's first searcher for that name, right after its loader, and its other names stay in `package.preload` (with `--fallback`, every name goes to `package.postload` as above):

```lua
local __luamerge_searchers = package.searchers or package.loaders
local __luamerge_first = __luamerge_searchers[1]
__luamerge_searchers[1] = function(name, ...)
  if name ~= "utf8" then return __luamerge_first(name, ...) end
  local found, data = __luamerge_first(name, ...)
  if type(found) == "function" then return found, data end
  return __luamerge_loader
end
```

## Limitations

- **Dynamic requires** (`require(someVariable)`, `pcall(require, name)`) are detected but can't be resolved statically — they produce a `dynamic-require` warning and are left as runtime requires.
- **Indirect uses of `require`** — aliasing it (`local r = require`), storing it in a table, passing it to a function — hide the calls made through it: they produce an `indirect-require` warning, and the modules loaded that way are embedded only if listed with `--include`. A presence check such as `if not require then` is reported the same way. Calls are recognized as `require(…)`, `_G.require(…)`, `_ENV.require(…)`, `pcall(require, …)` and `xpcall(require, handler, …)`. A local named `require` that is a function of its own — a local function, a local whose value is a function, a parameter or a loop variable — hides the global one for the rest of its scope, as in Lua, so the calls through it are no requires. A local of any other value is taken for the global `require` or a wrapper of it: `local require = require` and `local require = _G.require` hold the global, and a lazy require such as `local require = require("lazy")`, common in Neovim plugins, loads the modules of the names it gets, so the calls through it are found and their modules embedded. What such a local is assigned later does not count, but storing the global `require` in one that hides it is reported as an indirect use. `_G`, `_ENV`, `pcall` and `xpcall` are recognized by name.
- **C modules** (`.so`/`.dll`) are not embedded; they remain runtime requires.
- **Module names are decoded as Lua 5.2 and later decode them.** On Lua 5.1, which reads an escape it does not know as the character after the backslash, `require("\x61x")` loads the module `x61x`, but the bundler embeds `ax` (see [Finding requires](#finding-requires)).
- **A file the parser cannot read is scanned for its requires** (a `token-scan` warning), and the token scanner takes a local named `require` for the global one. Such a file usually has a syntax error; it is embedded all the same, so without `--debug` the bundle fails to load where the interpreter compiles it (see below).
- **No asset embedding** — only `.lua` modules are bundled.
- **Every module is compiled when the bundle loads.** Each module must compile on the interpreter that runs the bundle, even one the program only tries through `pcall(require, ...)`: natively such a module fails when it is required, in the bundle the whole bundle fails to load. With `--debug` a module is compiled when it is first required.
- **Modules inherit the bundle's environment.** Natively `require` runs a module in the global environment; in a bundle, modules and the entry run in the environment the bundle itself was loaded with, in both modes. That differs only when a host loads the bundle with an environment of its own (`setfenv`, or `load`'s `env` argument).
- **With `--suffix`, the entry runs one stack level deeper than a script**, inside a function the bundle calls and returns from before the suffix runs. A check that counts stack levels to tell whether it is the main script, such as `pcall(debug.getlocal, 4, 1)`, sees a module there on every interpreter but Tarantool. So does it with `--debug` on PUC Lua 5.1, where the bundle's call of the entry's chunk leaves a tail-call level.
- **Module code runs inside a vararg function.** On PUC Lua 5.1 and Lua 5.5 such a function keeps one of its 200 local variable slots for itself, so a module can declare at most 199 locals at its top level there (200 natively); the same holds for the entry with `--suffix`. On PUC Lua 5.1 an assignment to `arg` in a module (or, with `--suffix`, in the entry) sets that function's implicit `arg` local, not the global.
- **Tarantool's `override.<name>` modules are not modeled.** Natively Tarantool loads a module named `override.foo` (such as `override/foo.lua`) for `require("foo")` ahead of both `foo.lua` and a built-in `foo`; the bundler neither embeds such a module nor lets it take the place of an embedded one.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for build commands, code style, testing,
and the pull-request process. Run the test suite with `make test`; the tests
that execute generated bundles run them on every Lua interpreter on `PATH`
(see [Lua interpreters](CONTRIBUTING.md#lua-interpreters)).

## License

BSD 2-Clause — see [LICENSE](LICENSE).

[godoc-badge]: https://pkg.go.dev/badge/github.com/tarantool/go-luamerge.svg
[godoc-url]: https://pkg.go.dev/github.com/tarantool/go-luamerge
[actions-badge]: https://github.com/tarantool/go-luamerge/actions/workflows/testing.yaml/badge.svg
[actions-url]: https://github.com/tarantool/go-luamerge/actions/workflows/testing.yaml
[coverage-badge]: https://coveralls.io/repos/github/tarantool/go-luamerge/badge.svg?branch=master
[coverage-url]: https://coveralls.io/github/tarantool/go-luamerge?branch=master
[telegram-badge]: https://img.shields.io/badge/Telegram-join%20chat-blue.svg
[telegram-en-url]: http://telegram.me/tarantool
[telegram-ru-url]: http://telegram.me/tarantoolru
