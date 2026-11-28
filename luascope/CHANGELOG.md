# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic
Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- go-luascope moved into the monorepo of the Go Lua tooling of
  tarantool-contrib, as `luascope/` of the module
  `github.com/tarantool-contrib/go-moonscript`: the packages are
  `github.com/tarantool-contrib/go-moonscript/luascope`, `.../luascope/globals`
  and `.../luascope/allowlist`, the versions are the module's, and it needs
  Go 1.26.5.
- luascope analyzes the trees of the Lua front end of the monorepo: the
  parser `lua/parse` (a port of `lj_lex.c`, `lj_strscan.c` and
  `lj_parse.c` with the model of LuaJIT's code generator, which started as
  luascope's), the tree `lua/ast` and the name resolution `lua/scope` of the
  gopher-lua fork. The globals, the requires, the allow-list verdicts, their
  positions and the error messages are the same as before on the corpora,
  the random edits, the generated programs and the snippets of the tests,
  the archives of the monorepo and the parse-parity cases of the fork, 42
  thousand sources.
- New package `luascope`: `Parse(src, name, Options)` parses a chunk held
  in memory and resolves its names, and returns the `*ast.Chunk` of
  `lua/ast` and the `*scope.Info` of `lua/scope`, which a lint pipeline
  passes to every analysis. `Options.Dialect` is `LuaJIT`, the default
  (Tarantool's LuaJIT, as before), or `Lua51`, new. The chunk name follows
  LuaJIT's convention and is printed in messages as before; under `Lua51`
  as Lua 5.1 prints it.
- `globals`: `Analyze(chunk)` is `AnalyzeChunk(chunk, info)`, which takes
  the tree and its resolution, and `Analyze(src, name, opts)` parses the
  source itself. `Access` and `Require` have a `Span` (`ast.Span`, the byte
  offsets of the global name they start with) and their `Pos` is an
  `ast.Pos` of `lua/ast`: `Line` and `Column` (it was `Col`), and the
  `Offset`. `Usage.RequireValues` is a list of `Access` (it was of `ast.Pos`):
  the reads of `require` with their paths, also in `Accesses`.
- `allowlist`: `Checker.Check(chunk)` is `Checker.CheckChunk(chunk, info)`,
  and `Checker.Check(src, name, opts)` parses the source itself and returns
  its syntax error. `Violation` has the `Span` of the name it is at, and its
  `Pos` is an `ast.Pos`.
- A syntax error is a `*parse.Error` of `lua/parse` (it was a
  `*parser.SyntaxError`): `Pos.Line`, `Message` and `Token` (`Line`, `Msg`
  and `Near`), its `Pos.Source` is the chunk name as printed, and
  `Error()` gives the same message. A bytecode chunk is rejected with an
  error wrapping `luascope.ErrBytecode`, with the same message.
- A chain of 100000 fields or calls (`t.x.x...`, an expression of at least
  200 KB), which LuaJIT and luascope accepted, is rejected with the
  `*scope.Error` of `lua/scope`, which does not resolve a tree that deep, as
  the compiler of `lua/` does not compile it: it fails closed.
- The tree keeps the source given to `Parse`, without a copy, for the lines
  and columns of its offsets: the caller must not modify it while the tree
  is in use. It made a copy before.
- Speed (`BenchmarkAnalyze` and `BenchmarkAnalyzeChunk` on the 961 Lua
  files of the archives, 6.2 MB, on an AMD EPYC 7R43): parsing, resolving
  and analyzing them takes 327 ms (19 MB/s), 18% more than with luascope's
  parser (278 ms): the parser of the fork is as fast as luascope's, which
  resolved names while parsing, and the resolution of `lua/scope` is a pass
  of its own (67 ms), which a pipeline sharing the resolution pays once.
  The analysis of the trees parsed and resolved takes 43 ms, 23% less
  (56 ms).

### Removed

- The packages `parser` and `ast`, 5 thousand lines (3.6 thousand of code
  without the comments), and 1.4 thousand lines of their tests: the front
  end is `lua/parse` and `lua/ast` (see above). `parser.Parse` from an
  `io.Reader` and `parser.ReadError` are gone: read the source, then call
  `luascope.Parse`. The `ast` types have their counterparts in `lua/ast`:
  `Name` is `IdentExpr`, `Index` is `AttrGetExpr`, `Call` and `MethodCall`
  are `FuncCallExpr`, `Paren` is `Expr.Parens`, `Local` is a `scope.Var`,
  `ast.Inspect` is `ast.Inspect` of `lua/ast`.
- The test corpora in `parser/testdata/corpus`: the tests read the same Lua
  files from the archives of `internal/luasuites`. The comparison of the
  bytecode with LuaJIT's (`bytecode-*.golden.gz`): the model of the code
  generator in `lua/parse` keeps no constants to compare; its limits are
  tested there.

### Fixed

- `globals`: `require` named through the constant fields of `_G`, the table
  of globals, is reported like `require`: `_G.require("log")` and
  `pcall(_G["require"], "log")` are in `Requires`, and
  `local r = _G.require` is in `RequireValues`. They were only reads of the
  global path `_G.require`, so the rules of `allowlist` did not check the
  module when their globals allowed that path: with `*` or `_G.*` in the
  globals, `_G.require("os")` passed though `os` was not in the modules.

## [0.1.0] - 2026-10-22

The first public release.

### Added

- `parser`: parses a chunk of Lua code in the dialect of Tarantool's LuaJIT,
  performs the checks of the LuaJIT compiler, and resolves every name to a
  local variable, an upvalue, or a global variable. `Parse` reads the chunk
  from an `io.Reader` and `ParseBytes` takes it in memory; a failure to read
  the reader is reported as a `ReadError` with the chunk name.
- `ast`: the syntax tree of a chunk and `Inspect` to walk it.
- `globals`: reports the global variables, with the paths of their constant
  fields, and the `require` calls a chunk uses, matching the `GGET` and
  `GSET` instructions of the compiled chunk.
- `allowlist`: checks the globals and modules of a chunk against lists of
  allowed names, with wildcards for fields and submodules.

### Changed

- `parser`: the chunk name passed to `Parse` and `ParseBytes` follows the
  convention of LuaJIT, and error messages format it the way LuaJIT does:
  `"@file.lua"` is printed as `file.lua`, with the start cut off if it is
  long; `"=name"` is printed as `name`; any other name is taken for the
  source of the chunk and printed as `[string "name"]`. Pass `"@" + path`
  for a file to keep messages like `file.lua:3: ...`; a name passed as it
  was before is now printed as `[string "file.lua"]:3: ...`. A `ReadError`
  formats the name the same way. The errors of bytecode chunks name the
  chunk like the bytecode loader of LuaJIT, and the error for bytecode after
  a header does not name it, like LuaJIT. `SyntaxError.Chunk` and
  `ReadError.Chunk` keep the name as passed.
- `parser`: parsing takes 20-25% more time, because the parser emits the
  bytecode of LuaJIT to reject the code LuaJIT cannot encode, see below.
  On the 6.2 MB of Lua of the test corpus, `BenchmarkParseCorpus` went from
  76.4 to 62.9 MB/s on a 2-core x86-64 machine and from 127 to 101 MB/s on
  an Apple M4 Pro; the absolute times depend on the machine. On the first
  one, the files under 5 KB take 21.8 ms instead of 17.4. Parsers reuse
  their memory through a `sync.Pool`, and the names, numbers and comments
  of the AST are substrings of one copy of the source, which they keep
  alive: a parse allocates 23% fewer objects and 4% more bytes.

### Fixed

- `globals`: a string constant in parentheses used as a key is a constant
  field, as it is for LuaJIT: `box[("schema")].space.create()` is a read of
  `box.schema.space.create`, not of `box[?].space.create`.
- `globals`: a chain of fields is analyzed once, so the time and memory of
  `Analyze` are linear in its length. A chain rooted at a local variable or
  a call, like `local t = {} return t.x.x.x`, was analyzed again from each
  of its fields, which was quadratic: 10000 fields took 1.5 seconds.
- `parser`: the limit of LuaJIT on the variable stack is checked. A chunk
  with more than 65476 locals, gotos and labels declared in a function and
  the functions enclosing it, active or not, is rejected with LuaJIT's
  error "chunk has more than 65476 local variables"; it was accepted.
- `parser`: the code that the code generator of LuaJIT cannot encode is
  rejected with the errors of LuaJIT; it was accepted. That is a jump over
  more than 32767 instructions ("control structure too long"), a function
  that needs more than 250 registers ("function or expression too
  complex"), a return too far from the end of its function to be fixed up
  ("function too long for return fixup"), more than 65536 constants in a
  function, more than 2^26 instructions in the functions being compiled.

[Unreleased]: https://github.com/bigbes/go-luascope/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/bigbes/go-luascope/releases/tag/v0.1.0
