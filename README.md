[![Status: maintained](https://img.shields.io/badge/status-maintained-brightgreen)](https://github.com/tarantool-contrib/rfcs/blob/master/text/0002-governance.md#module-status)
[![CI](https://github.com/tarantool-contrib/go-moonscript/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/tarantool-contrib/go-moonscript/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tarantool-contrib/go-moonscript.svg)](https://pkg.go.dev/github.com/tarantool-contrib/go-moonscript)

# go-moonscript

A native Go port of [MoonScript](https://moonscript.org) v0.8.0, the
language that compiles to Lua. It needs no Lua, LuaRocks or C toolchain:

- **compiler** — MoonScript to Lua, with output byte-identical to upstream
  `moonc` 0.8.0: the same Lua code, the same line tables, the same parse and
  compile error messages;
- **`moonc`** — the command-line compiler, with the upstream flags (`-p`,
  `-o`, `-t`, `-T`, `-X`, `-b`, `--correlate`, stdin) and the linter
  (`moonc -l`);
- **`moon`** — runs MoonScript scripts on [gopher-lua](https://github.com/yuin/gopher-lua)
  (a Lua 5.1 VM in Go), with upstream's error rewriting (tracebacks point at
  `.moon` lines), the `.moon` module loader and `moon -c` coverage;
- **packages** for Go programs: the compiler (`moonscript`), the parser
  (`parse`), the linter (`lint`) and the Lua runtime (`runtime`).

It is for Go programs and tools that need to compile or run MoonScript:
build pipelines, editors and language tooling, applications embedding Lua.
It does not depend on Tarantool.

## Installation

### The commands

```sh
go install github.com/tarantool-contrib/go-moonscript/cmd/moonc@latest
go install github.com/tarantool-contrib/go-moonscript/cmd/moon@latest
```

`go install` puts `moonc` and `moon` into `$(go env GOPATH)/bin` (or
`$GOBIN`); add that directory to `PATH`. Use a release tag instead of
`@latest` to pin a version, e.g. `@v0.1.0`. There are no prebuilt binaries
yet.

From a checkout:

```sh
git clone git@github.com:tarantool-contrib/go-moonscript.git
cd go-moonscript
go build ./cmd/moonc ./cmd/moon     # ./moonc and ./moon
```

### The library

```sh
go get github.com/tarantool-contrib/go-moonscript@latest
```

The module has no dependencies outside the Go standard library.

Supported Go versions: **1.26 and 1.27** (the two latest releases; CI
tests both).

## Quick start

```sh
$ cat hello.moon
greet = (name) -> print "hello #{name}"
greet "world"
$ moonc -p hello.moon
local greet
greet = function(name)
  return print("hello " .. tostring(name))
end
return greet("world")
$ moon hello.moon
hello world
$ moonc hello.moon      # writes hello.lua
$ echo 'x = 1' > bad.moon
$ moonc -l bad.moon     # lint
bad.moon

line 1: assigned but unused `x`
===============================
> x = 1

```

## Usage

### moonc

`moonc` behaves like upstream `moonc` 0.8.0:

```sh
moonc hello.moon              # compile to hello.lua, next to the source
moonc src/                    # every .moon file under src/, recursively
moonc -t build/ src/          # write the .lua files under build/ (build/src/...)
moonc -o out.lua hello.moon   # to a given file
moonc -p hello.moon           # print the Lua code
echo 'print ...' | moonc -    # compile standard input, print the Lua code
moonc --correlate -p f.moon   # Lua lines at the same line numbers as the source
moonc -T hello.moon           # print the parse tree
moonc -X hello.moon           # print the Lua line -> source position map
moonc -l src/                 # lint instead of compiling
moonc -h                      # all options
```

A file that fails to compile is reported on standard error, as
`hello.moon	failed to parse at line 1, column 5: ...`, and `moonc` exits
with status 1.

The linter reports global accesses, unused variables, assignments to
constants and imports that overwrite a name; `--lint-stage` and
`--exclude-lint-stage` select the checks, `--lint-format compact` prints
one `file:line:col: message` line per report. Extra allowed globals per file come
from a `lint_config.lua` module found on `package.path` (`./lint_config.lua`
first), as upstream does:

```lua
return {
  whitelist_globals = {
    ["spec/"] = { "describe", "it", "assert" }, -- Lua pattern -> globals
  },
}
```

### moon

```sh
moon script.moon arg1 arg2    # run a script; arg and ... hold the arguments
moon -e 'print "sum", 1 + 2'  # run a string
moon -c script.moon           # run, then print a line coverage report
moon -d script.moon           # don't rewrite error tracebacks to .moon lines
```

`require "name"` finds MoonScript modules along `package.moonpath`, which
is derived from `package.path` (`./?.lua` gives `./?.moon`), and Lua
modules along `package.path`. Errors point at the `.moon` source:

```
$ cat script.moon
f = ->
  -- comment
  error "boom"
f!
$ moon script.moon
moon: script.moon:3: (3) boom
stack traceback:
	[C]: in function 'error'
	script.moon:3: (3) in function 'f'
	script.moon:4: (5) in main chunk
```

(`(3)` is the line of the compiled Lua.) Only pure Lua modules can be
loaded: there are no C modules (and no `lpeg`).

### Compiling from Go

```go
import moonscript "github.com/tarantool-contrib/go-moonscript"

code, posmap, err := moonscript.ToLua(`greet = (name) -> print "hello #{name}"`, moonscript.Options{})
if err != nil {
	// err.Error() is moonc's message:
	// failed to parse at line 1, column 5:
	//   x = = 1
	//       ^
	return err
}
// code is the Lua source; posmap maps its line numbers to byte offsets in
// the MoonScript source
```

`moonscript.Options` has upstream's compile options: `Correlate` (with
`Source`) for `--correlate`, and `NoImplicitReturnRoot`. A parse error is a
`*parse.Error`, a compile error a `*moonscript.Error` with the position.

Linting from Go:

```go
import "github.com/tarantool-contrib/go-moonscript/lint"

report, found, err := lint.Code(src, "input.moon", nil, lint.Options{})
// found: there is something to report; report is moonc -l's text
```

### Running MoonScript from Go

The `runtime` package runs MoonScript on the gopher-lua in this module:

```go
import (
	"github.com/tarantool-contrib/go-moonscript/runtime"
	lua "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua"
)

L := runtime.NewState() // standard libraries, and the moonscript and moon modules
defer L.Close()

// a Go function, callable from MoonScript
L.SetGlobal("greet", L.NewFunction(func(L *lua.LState) int {
	L.Push(lua.LString("hello, " + L.CheckString(1)))
	return 1
}))

// the .moon loader, for require (moon and require "moonscript" install it too)
if _, err := runtime.InsertLoader(L); err != nil {
	return err
}

fn, err := runtime.LoadString(L, `print greet "moon"`, "=example", runtime.Options{})
if err != nil {
	return err // a compile error
}
L.Push(fn)
if err := L.PCall(0, 0, nil); err != nil {
	return err // a runtime error
}
```

`runtime.LoadFile` and `runtime.DoFile` load `.moon` files (chunk name
`@path`, so errors name the file); `runtime.RewriteTraceback` and
`ReverseLineNumber` map Lua lines back to MoonScript lines, as `moon` does.
In Lua, `require "moonscript"` gives upstream's `moonscript.base` API
(`to_lua`, `loadstring`, `loadfile`, `dofile`, ...). See the
[package documentation](https://pkg.go.dev/github.com/tarantool-contrib/go-moonscript/runtime)
and the [examples](example_test.go).

The VM implements one of two Lua dialects, chosen when the state is
created:

```go
L := runtime.NewState()                                    // PUC Lua 5.1.5 (the default)
L := runtime.NewState(runtime.StateOptions(lua.DialectLuaJIT)) // LuaJIT 2.1
```

`DialectLua51` behaves as PUC Lua 5.1.5: its syntax, libraries, error
messages, `math.random` sequence (glibc's `rand`, as PUC on Linux) and
`#`/`next` on tables. `DialectLuaJIT` adds what LuaJIT 2.1 adds without its
5.2 compatibility mode: `goto` and labels, `\x`/`\z`/`\u{}` escapes, hex
floats, the `bit` library, a `jit` module (interpreter only),
`table.new`/`table.clear`, `load` with strings and modes, `xpcall` with
arguments and so on, with LuaJIT's number formatting and `math.random`.
There is no FFI. `moon` runs in the Lua 5.1 dialect, as the reference does.

**Use this module's gopher-lua, not `github.com/yuin/gopher-lua`.** The Lua
types of the API (`*lua.LState`, `lua.LValue`, ...) belong to the patched
copy in `third_party/gopher-lua`. They are not interchangeable with the
upstream package's types, so a program using both cannot pass states or
values between them.

## Compatibility

The port is checked against the reference implementation — upstream
MoonScript v0.8.0 on PUC Lua 5.1, built by [`scripts/moonref`](scripts/moonref):

- upstream's own specs: the compiler spec inputs and outputs, the parser and
  error specs, and the behavioural specs (run on gopher-lua);
- every code example of the MoonScript documentation;
- parse trees and error messages of about 500 hand-written edge cases, and
  differential fuzzing of the parser;
- a corpus of 19,712 real-world MoonScript files from 2,324 GitHub
  repositories (2.6 million lines) plus 55,595 mutated copies of them as
  parse and compile error cases: the parse trees, the Lua output, the error
  messages and the lint reports all match.

Known differences:

- **`moon`** runs on gopher-lua, not PUC Lua: tracebacks are cut to fewer
  frames and there are no C modules (no `lpeg`). The VM's runtime error
  messages, `math.random` sequence and `pairs` order are Lua 5.1's, and
  the file and line in a rewritten error are the same.
- **`moon -c`** prints a coverage report; upstream 0.8.0 prints an empty
  one on Lua 5.1 (its line hook is a tail call). This is fixed on purpose.
- **`moonc --watch` and `--transform`** are accepted but not supported.

gopher-lua is vendored in [`third_party/gopher-lua`](third_party/gopher-lua)
and patched towards PUC Lua 5.1's behaviour: a fix for upvalues closed by
errors caught with `pcall`, `debug.sethook`/`debug.gethook`, luac's line
numbers, Lua 5.1's table layout (`#`, `next`, sparse keys) and pattern
matcher, coroutine and compiler fixes, Lua 5.1's runtime error messages,
`string.format`, number formatting,
and fixes collected from other gopher-lua forks and pull requests. Every
change, with its source, is in
[`moonscript.patch`](third_party/gopher-lua/moonscript.patch); the official
Lua 5.1 and LuaJIT test suites run against it in
[`internal/luasuites`](internal/luasuites). On LuaJIT's benchmark suite it
is about 3.3 times slower than PUC Lua 5.1 (geometric mean; MoonScript
workloads about 2.6 times).

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md): running the checks, regenerating the
generated code and the reference outputs, and the corpus tools.

## License

BSD-2-Clause, see [LICENSE](LICENSE). The port is derived from MoonScript
and includes a modified gopher-lua, both under the MIT license; see
[NOTICE](NOTICE).
