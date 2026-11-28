# go-luascope

[![CI](https://github.com/tarantool-contrib/go-moonscript/actions/workflows/ci.yml/badge.svg)](https://github.com/tarantool-contrib/go-moonscript/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tarantool-contrib/go-moonscript/luascope.svg)](https://pkg.go.dev/github.com/tarantool-contrib/go-moonscript/luascope)

Static analysis of Lua code written for [Tarantool](https://www.tarantool.io).

Lua code that runs inside Tarantool can reach the whole server: the `box`
API, the fibers, the modules installed on the instance. A tool that runs
untrusted code, checks a migration before applying it, or reviews a stored
procedure needs to know which globals and which modules that code touches,
and needs a good answer for code that does not even compile. `go-luascope`
answers both: it parses Lua the way Tarantool's LuaJIT does, with the checks
of the LuaJIT compiler, and it resolves every name to a local variable, an
upvalue or a global. On top of that, it reports which global variables and
modules the code uses and checks them against allow-lists.

| Package | Purpose |
|:---|:---|
| `luascope` | Parses and resolves a chunk: `Parse`, the dialects, the chunk names. |
| `globals` | Global variables, their fields, and `require` calls used by a chunk. |
| `allowlist` | Checks the globals and modules of a chunk against allow-lists. |

The Lua front end is the monorepo's: the parser (`lua/parse`), the syntax
tree (`lua/ast`) and the name resolution (`lua/scope`) of the gopher-lua
fork in [`lua/`](../lua), which the MoonScript port and go-luamerge use too.

go-luascope lives in `luascope/` of the monorepo of the Go Lua tooling of
tarantool-contrib (it was [bigbes/go-luascope](https://github.com/bigbes/go-luascope)):
its packages are `github.com/tarantool-contrib/go-moonscript/luascope/...`, its
versions are the module's.

## Quick start

```sh
go get github.com/tarantool-contrib/go-moonscript
```

```go
package main

import (
	"fmt"
	"os"

	"github.com/tarantool-contrib/go-moonscript/luascope"
	"github.com/tarantool-contrib/go-moonscript/luascope/allowlist"
	"github.com/tarantool-contrib/go-moonscript/luascope/globals"
)

func main() {
	src, err := os.ReadFile("migration.lua")
	if err != nil {
		fmt.Println(err)
		return
	}

	// Parse and resolve once, for every analysis.
	chunk, info, err := luascope.Parse(src, "@migration.lua", luascope.Options{})
	if err != nil {
		// A LuaJIT-like message: "migration.lua:3: unexpected symbol near 'x'".
		fmt.Println(err)
		return
	}

	for _, require := range globals.AnalyzeChunk(chunk, info).Requires {
		fmt.Println(require.Pos, require.Module) // 1:14 json
	}

	checker, err := allowlist.NewChecker(allowlist.Rules{
		Modules: []string{"crud", "json"},
		Globals: []string{"require", "string.*", "box.space.*"},
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, v := range checker.CheckChunk(chunk, info) {
		fmt.Println(v) // Nothing: the chunk uses only allowed names.
	}
}
```

`globals.Analyze(src, name, opts)` and `checker.Check(src, name, opts)` do
the same from the source, parsing it themselves.

## Dialect

The default dialect, `luascope.LuaJIT` (the zero `luascope.Options`), is
that of [tarantool/luajit](https://github.com/tarantool/luajit), built the
way Tarantool builds it: with FFI and without `LUAJIT_ENABLE_LUA52COMPAT`.
The lexer and the parser of `lua/parse` are ports of `lj_lex.c`,
`lj_strscan.c` and `lj_parse.c` in that dialect. Besides Lua 5.1, this
means:

- `goto` and `::labels::` with LuaJIT's scoping rules; `goto` is a keyword
  only before a name, so `local goto = 1` is valid;
- `\xXX`, `\z` and `\u{XXX}` escapes;
- binary literals `0b101`, hexadecimal fractions and exponents `0x1.8p1`;
- `1i`, `1LL`, `1ULL` and `1LLU` number suffixes;
- any byte 128-255 in identifiers, a UTF-8 BOM and a `#!` line;
- a `break` must be the last statement of a block, there are no empty
  statements (`;;`), and a call cannot start on a new line
  (`ambiguous syntax`), as in Lua 5.1.

`luascope.Lua51` is PUC Lua 5.1's dialect (`lparser.c`), in which a vararg
function has the implicit local `arg`.

`luascope.Parse` takes the source in memory, and the tree keeps it, without
a copy, for the lines and columns of its offsets: the source must not be
modified while the tree is in use. The chunk name follows the convention of
LuaJIT and is printed in error messages the way LuaJIT prints it:
`"@migration.lua"` names a file and is printed as `migration.lua`, with the
start cut off if it is long; `"=name"` is printed as `name`; any other name
is taken for the source of the chunk and printed as `[string "name"]`. A
syntax error is a `*parse.Error`, with the line, the message and the token.

The parser does the compiler's checks: gotos and labels, `break` outside a
loop, `...` outside a vararg function, assignment targets, and the limits on
local variables, upvalues and syntax nesting. The limits on local variables
are both of LuaJIT's: 200 active in a function, and 65476 locals, gotos and
labels declared in a function and the functions enclosing it.

The parser also emits the bytecode LuaJIT emits for the chunk, with the same
registers and jumps, and drops it as soon as a function ends. This way it
rejects the code that the code generator of LuaJIT cannot encode: a jump
over more than 32767 instructions (`control structure too long`), a
function that needs more than 250 registers (`function or expression too
complex`), more than 65536 constants in a function.

Error messages, lines and the tokens they are reported near are the same as
LuaJIT's. Differences from LuaJIT:

- bytecode chunks are always rejected (`ErrBytecode`); they cannot be
  analyzed;
- a chain of 100000 fields or calls (`t.x.x...`, at least 200 KB of one
  expression) is rejected: the name resolution stops at a tree that deep,
  as the compiler of `lua/` does;
- `^` on two number constants is folded with Go's `math.Pow`, while LuaJIT
  uses `pow` of the C library, so the folded constant can differ in the
  last bit. The constant only decides whether it is shared with an equal
  one, which changes no error in practice.

## Globals and allow-lists

`globals.Analyze` reports one access per use of a global name, with the path
of constant fields after it: `box.space.users:select()` is a read of
`box.space.users.select`, `box.space[name]` is a read of `box.space[?]`,
`t.x = 1` is a write of `t.x` when `t` is global. A string constant in
parentheses is a constant field too: `box[("space")]` is `box.space`. The
accesses match the `GGET` and `GSET` instructions LuaJIT emits for the chunk.
Each has the `ast.Span` of the global name it starts with and the `ast.Pos`
(line and column) of its start.

Calls of the global `require` are reported with the module name when it is a
string constant or a concatenation of string constants: `require("log")`,
`pcall(require, "log")` and `xpcall(require, handler, "log")`. A `require`
named through the constant fields of `_G`, the table of globals, is the
same: `_G.require("log")` and `pcall(_G["require"], "log")`. Other uses of
`require`, like `local r = require` or `local r = _G.require`, are reported
separately, because the module they load cannot be known.

An entry of `allowlist.Rules` allows exactly the module or the global path it
names. A `*` element matches any one name, including a field indexed with a
non-constant key, and a `*` at the end matches one or more names:

| Entry | Allows | Does not allow |
|:---|:---|:---|
| `string` | `local s = string` | `string.format()` |
| `string.*` | `string.format()` | `local s = string` |
| `box.space.*` | `box.space.users:select()`, `box.space[name]` | `pairs(box.space)` |
| `box.space.*.select` | `box.space.users:select()`, `box.space[name]:select()` | `box.space.users:replace()` |
| `crud` | `require("crud")` | `require("crud.common")` |
| `crud.*` | `require("crud.common.utils")` | `require("crud")` |

Writing global variables is never allowed. A `require` with a computed module
name and `require` used as a value are violations.

The analysis is static and is not a sandbox: code can still reach globals
through allowed functions, like `rawget` or `getfenv`, if the lists allow
them. The same goes for `_G`, the table of globals. An entry that allows
`_G` itself, like `_G` for `rawget(_G, name)` or `*`, lets code take the
table and reach any global and any module through it:
`local g = _G g.require("os")` passes. An entry that allows `_G` with a
non-constant key, like `_G.*`, lets `_G[name]("os")` pass. Named with
constant keys, `_G.require` is still `require`: `_G.require("os")` is
checked against the modules.

## Documentation

The reference is on [pkg.go.dev](https://pkg.go.dev/github.com/tarantool-contrib/go-moonscript/luascope),
including runnable examples for every package. Locally:

```sh
go doc ./allowlist  # the package documentation of any package
pkgsite -http=:6060  # or: go run golang.org/x/pkgsite/cmd/pkgsite@latest
```

`go doc` and `pkgsite` render the same comments that
[pkg.go.dev](https://pkg.go.dev/github.com/tarantool-contrib/go-moonscript/luascope) shows.

## Requirements

- Go 1.26.5 or later (the module's), no C toolchain and no Tarantool
  installation: the library is pure Go.
- The packages depend on the fork's `lua/ast`, `lua/parse` and `lua/scope`
  of the same module and on nothing outside it; the tests use
  `github.com/stretchr/testify`.
- Linux, macOS and Windows are supported; CI runs the tests on Linux.

## Testing

`luascope.Parse` is compared with LuaJIT on:

- the Lua 5.1 test suite, the LuaJIT, lua-Harness and Tarantool tests of
  tarantool/luajit, and the Lua sources of Tarantool, vshard and crud: the
  Lua files of the archives of
  [`internal/luasuites`](../internal/luasuites/testdata/archives/SOURCES.md);
- 4 random edits of every corpus file;
- 2000 generated programs that mix scopes, closures, loops and gotos;
- hand-written snippets for each lexer and parser rule.

For each source the result must match: either the same error, or the same
global reads and writes as the `GGET` and `GSET` instructions of the
compiled chunk. The reference results are stored in `testdata/golden`, so
the tests do not need LuaJIT. To regenerate them, build the LuaJIT of
Tarantool and run:

```sh
git clone --depth 1 https://github.com/tarantool/luajit /tmp/luajit
rm -rf /tmp/luajit/.git  # A shallow clone has no tags for the version.
cmake -S /tmp/luajit -B /tmp/luajit-build -DLUAJIT_ENABLE_GC64=ON
make -C /tmp/luajit-build -j
make golden LUAJIT=/tmp/luajit-build/src/luajit
```

The bytecode the parser emits for the limits of LuaJIT's code generator is
tested in `lua/parse` (`jitcode_test.go`), with the limit snippets.

`make test` runs the tests, `make testrace` runs them a hundred times with
the race detector, `make cover` prints the statement coverage, `make lint`
runs golangci-lint, `make spell` runs codespell, and `make fuzz` runs the
fuzzer of `Parse` and the analyses, all in `luascope/`.

## Contributing

Issues and pull requests are welcome, see [CONTRIBUTING.md](CONTRIBUTING.md).
Changes are listed in [CHANGELOG.md](CHANGELOG.md).

## License

BSD 2-Clause, see [LICENSE](LICENSE). The test corpora keep their own
licenses, see [SOURCES.md](../internal/luasuites/testdata/archives/SOURCES.md).
