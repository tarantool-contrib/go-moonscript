# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

The first version: a native Go port of MoonScript v0.8.0.

### Added

- The compiler, `moonscript.ToLua`: MoonScript to Lua, with output
  byte-identical to upstream `moonc` 0.8.0, including line tables
  (`--correlate` too) and parse and compile error messages.
- The parser (`parse`), generated from a transcription of the upstream
  grammar, with upstream's parse trees and error positions.
- The linter (`lint`, `moonc -l`): global access, unused variables, constant
  assignment and import overwrite checks, `lint_config` whitelists, both
  output formats.
- `moonc`, with upstream's options: `-p`, `-o`, `-t`, `-T`, `-X`, `-b`,
  `--correlate`, stdin, directories, and the same usage and error texts.
- `moon` and the `runtime` package: MoonScript on gopher-lua, with the
  upstream Lua modules (`moonscript`, `moonscript.base`, `moonscript.errors`,
  `moonscript.util`, `moon`, ...), the `.moon` loader, line tables and
  traceback rewriting, and `moon -c` coverage.
- A patched gopher-lua v1.1.2 in `third_party/gopher-lua`, moved towards
  PUC Lua 5.1 (and LuaJIT 2.1's extensions where they don't change 5.1
  programs), each change tested against real Lua 5.1/LuaJIT and listed with
  its source in `moonscript.patch`:
  - upvalues are no longer closed for frames that survive an error caught
    by `pcall`; `debug.sethook`/`debug.gethook`; luac's line numbers;
  - the table engine in Lua 5.1's layout (`#`, `next`, `pairs` order, sparse
    integer keys without huge arrays), about a third faster;
  - Lua 5.1's pattern matcher (frontier patterns, set syntax, limits,
    cancellation, linear `gsub`, a pattern cache), about twice as fast;
  - coroutine fixes (yield boundaries, resume values, error texts, context
    leaks); compiler fixes (evaluation order, jumps, `for`, upvalue
    limits, numerals); `string.format`, number formatting, `tonumber`,
    `os.date`, `io.open`/`popen`, `table.*` as in Lua 5.1;
  - fixes collected from other gopher-lua forks and pull requests.
- A dialect option for the VM, `lua.Options{Dialect: ...}` /
  `runtime.StateOptions`: PUC Lua 5.1.5 (the default) or LuaJIT 2.1 (its
  syntax and library extensions: `goto`, `bit`, `jit`, `table.new`, ...;
  no FFI).
- `MOON_LUA=luajit` runs `moon` in the LuaJIT 2.1 dialect, checked against
  upstream `moon` run by `luajit` (`testdata/*/ref_luajit/`); unset or
  `lua51` is Lua 5.1, any other value an error. Upstream's command line is
  unchanged.
- PUC Lua 5.1's error messages (variable names, argument errors, error
  levels), syntax error messages and limits (a port of `lparser.c` /
  `lj_parse.c` replaces the yacc grammar), the missing 5.1 functions
  (`gcinfo`, `table.foreach`, `string.dump`, ...), liolib's io with
  buffered stdout, `math.random` sequences equal to PUC's (and LuaJIT's in
  its dialect), weak tables, `__gc` and `newproxy`, and an optional memory
  limit (`Options.MemoryLimit`) that raises "not enough memory".
- `internal/luasuites`: the official Lua 5.1 tests, lua-Harness and
  LuaJIT-tests run against the VM, with every outcome pinned.
- `internal/tools/luabench`: LuaJIT's benchmark suite and MoonScript
  benchmarks on the VM, PUC Lua 5.1 and LuaJIT.
- The real-world corpus check in CI (the `corpus` job,
  `scripts/check-corpus.sh`): 19,960 MoonScript files of 1,609 GitHub
  repositories and 55,595 mutants of them, against upstream moonc 0.8.0's
  parse trees, Lua output, error messages and lint reports. The sources
  are not committed: `testdata/corpus/manifest.tsv` pins them (repository,
  commit, path, sha256, git blob id), `testdata/corpus/expected.tsv` holds
  digests of the reference outputs, `corpus fetch` fetches them (skipping
  repositories that are gone) and CI caches them;
  `scripts/gen-corpus-manifest.sh` regenerates both files from a full
  corpus.

### Changed

- `moon -c` prints the coverage report; upstream 0.8.0's is always empty on
  Lua 5.1, because its line hook is a tail call.
