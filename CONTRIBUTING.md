# Contributing to go-moonscript

The organization-wide rules of
[tarantool-contrib](https://github.com/tarantool-contrib/.github/blob/master/CONTRIBUTING.md)
apply (reporting bugs, pull requests, review, license); this file adds what
is specific to this repository.

## The rule: same output as upstream

go-moonscript is a port: for every input, the parser, the compiler and the
linter produce what upstream MoonScript v0.8.0 produces — the same Lua, the
same parse trees, the same error messages. A change of the output is a bug,
unless it is a deliberate, documented difference (README, *Compatibility*).

A bug report is most useful with the MoonScript input and the output of
both `moonc` from this repository and the reference (see below).

## Checks

What CI runs ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)):

```sh
go test ./...                       # everything, no network or Docker needed
go test -race ./...
go vet $(go list ./... | grep -v /third_party/)
golangci-lint run                   # v2.15.0, configured in .golangci.yml
go generate ./... && git diff --exit-code   # generated files up to date
go mod tidy -diff
```

Every change comes with a test; a fix comes with a test that fails without
it. Most tests compare against reference outputs stored in `testdata/`, so a
new case is usually a new input file plus its generated reference output.

## Layout

| Path | What |
| --- | --- |
| `moonscript.go` | `ToLua`, the compiler entry point |
| `ast/` | the syntax tree: Lua-table-like nodes, 1-based like upstream |
| `internal/grammar/` | the MoonScript grammar (upstream `moonscript/parse/grammar.moon`) as data |
| `parse/` | the parser; `parser_gen.go` is generated from `internal/grammar` |
| `compiler/` | transforms and code generation (`moonscript/transform`, `moonscript/compile`) |
| `lint/` | the linter (`moonscript/cmd/lint.moon`) |
| `runtime/` | the Lua runtime modules on gopher-lua; `runtime/lua` is generated from `runtime/moonsrc` |
| `cmd/moonc`, `cmd/moon` | the commands |
| `third_party/gopher-lua/` | patched gopher-lua v1.1.2, see `moonscript.patch` |
| `internal/tools/` | generators, the fuzzer and the corpus checker |
| `scripts/` | generators of the reference outputs in `testdata/` |

## Generated code

`go generate ./...` regenerates `parse/parser_gen.go` (from
`internal/grammar`, by `internal/tools/genparser`) and `runtime/lua` (the
upstream `.moon` library sources of `runtime/moonsrc`, compiled by the
port). Commit the result with the change that caused it.

`third_party/gopher-lua` has generated files of its own (`vm.go`,
`state.go`, `parse/parser.go`); `moonscript.patch` explains how to
regenerate them. A change there also updates `moonscript.patch`, the full
diff against the original v1.1.2: `scripts/gen-gopher-lua-patch.sh`
regenerates its diff part (edit its header, the list of changes and where
they come from, by hand); CI checks it with `--check`. A fix for Lua
behaviour comes with a script in `third_party/gopher-lua/testdata/compat`
whose reference output is recorded from real Lua 5.1 (and LuaJIT for
`*.jit.lua`) by `scripts/gen-compat-ref.sh`.

## The reference implementation

The reference outputs in `testdata/` come from upstream MoonScript v0.8.0 on
PUC Lua 5.1, in a Docker image. Build it, and unpack the same upstream
sources for the generators that copy upstream files:

```sh
scripts/moonref/build.sh        # image moonref:0.8.0, sources in ~/.cache/go-moonscript/
```

Then regenerate what a change affects:

| Script | Regenerates |
| --- | --- |
| `scripts/gen-parse-golden.sh` | `testdata/parse/`: parse trees and parse errors |
| `scripts/gen-docs-golden.sh` | `testdata/docs/`: the documentation examples |
| `scripts/gen-upstream-spec-ref.sh` | `testdata/upstream_spec/`: upstream's behavioural specs |
| `scripts/gen-lint-golden.sh` | `testdata/lint/` |
| `scripts/gen-moon-runner-ref.sh` | `testdata/moon_runner/`, `testdata/moon_smoke/` |
| `scripts/gen-hook-ref.sh` | `runtime/testdata/hooks/` |
| `scripts/gen-compat-ref.sh` | `third_party/gopher-lua/testdata/compat/ref/`: Lua behaviour tests of the VM |
| `scripts/gen-pattern-fuzz.sh` | the pattern-matching differential test (`lib_pattern_fuzz.lua`) |
| `scripts/gen-luasuites-ref.sh` | `internal/luasuites/testdata/`: the Lua 5.1 and LuaJIT test suites' references and the ledger (`--ledger`: the ledger only) |

A regenerated reference output that changes is a change of upstream
behaviour, which shouldn't happen: look at the diff before committing it.

## Lua test suites and benchmarks

- `go test ./internal/luasuites` runs the official Lua 5.1 tests,
  lua-Harness and LuaJIT-tests on the VM and compares every file and
  assertion with the outcomes pinned in `testdata/expected.tsv`: a new
  failure fails, and so does a new pass ("update the ledger": run
  `scripts/gen-luasuites-ref.sh --ledger` and commit the result).
  `LUASUITES_FULL=1` adds the slow files.
- `scripts/luabench.sh` runs LuaJIT's benchmark suite and
  `testdata/bench/moon` on the VM, PUC Lua 5.1 and LuaJIT (Docker), checks
  that every output equals PUC's, and prints the CPU times; `-compare
  old.json` compares with an earlier run.
- `scripts/fuzz-tables.sh` runs the table differential fuzzer with other
  seeds.

## Fuzzing and the corpus

- `go run ./internal/tools/parsefuzz -h`: differential fuzzing of the parser
  against the reference (Docker).
- `go test ./parse -fuzz FuzzDifferential`: the generated parser against
  the interpreter it replaced, no Docker needed.
- `internal/tools/corpus`: the real-world corpus check. `fetch.sh` clones
  the MoonScript repositories found on GitHub, `corpus collect` deduplicates
  them and derives error-case mutants, `corpus ref` runs the reference
  (Docker), `corpus run` compares. Run it before changes to the parser or the
  compiler; see the comment at the top of
  [`internal/tools/corpus/main.go`](internal/tools/corpus/main.go).

## Commits

- One logical change per commit, with a message in the form
  `prefix: short summary` (`parse: ...`, `compiler: ...`, `runtime: ...`,
  `ci: ...`), and a body that explains why.
- Everything is in English: code, comments, documentation, commit messages.
- User-visible changes go to [`CHANGELOG.md`](CHANGELOG.md) under
  `Unreleased`.
