# Contributing to go-moonscript

The organization-wide rules of
[tarantool-contrib](https://github.com/tarantool-contrib/.github/blob/master/CONTRIBUTING.md)
apply (reporting bugs, pull requests, review, license); this file adds what
is specific to this repository.

The repository is the monorepo of the Go Lua tooling (see the README): the
gopher-lua fork in `lua/`, the MoonScript port in `moonscript/` and
`cmd/`, and luascope and luamerge, imported with their history. This file
is about the fork and the MoonScript port; luascope and luamerge keep their
own rules in [`luascope/CONTRIBUTING.md`](luascope/CONTRIBUTING.md) and
[`luamerge/CONTRIBUTING.md`](luamerge/CONTRIBUTING.md), which apply in
their trees. It is one Go module: `go test ./...` at the root tests
everything, and `go test ./luamerge/...` one tree.

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
go test ./...                       # everything, no Docker needed
go test -race ./...
go vet $(go list ./... | grep -v '/lua$')   # the fork's VM has 4 known findings
gofmt -l .                          # prints nothing
golangci-lint run                   # v2.15.0: .golangci.yml, all but luascope/ and luamerge/
(cd luascope && golangci-lint run)  # luascope/.golangci.yml
(cd luamerge && golangci-lint run)  # luamerge/.golangci.yml
codespell ./luascope ./luamerge     # .codespellrc
go generate ./... && git diff --exit-code   # generated files up to date
scripts/gen-gopher-lua-patch.sh --check     # lua/moonscript.patch up to date
scripts/check-gopher-lua-suite.sh   # pristine gopher-lua's suites on the fork
go mod tidy -diff
```

luamerge's tests run its bundles on every Lua interpreter on `PATH` (CI
installs lua5.1 to lua5.4 and luajit and sets `LUATEST_INTERPRETERS`) and
skip those tests when there is none. The corpus tests of luamerge read the
Lua 5.1 and gopher-lua test suites of the gopher-lua v1.1.2 module, which
`go mod download` fetches into the module cache.

The root config of golangci-lint lints the fork's own packages
(`lua/scope`, `lua/format`, `lua/flow`, `lua/sandbox`) but not upstream's
code (the package `lua`, `lua/ast`, `lua/parse`, `lua/pm`), kept close to
upstream.

Every change comes with a test; a fix comes with a test that fails without
it. Most tests compare against reference outputs stored in
`moonscript/testdata/`, so a new case is usually a new input file plus its
generated reference output.

## Layout

| Path | What |
| --- | --- |
| `moonscript/moonscript.go` | `ToLua`, the compiler entry point (package `moonscript`) |
| `moonscript/ast/` | the syntax tree: Lua-table-like nodes, 1-based like upstream |
| `internal/grammar/` | the MoonScript grammar (upstream `moonscript/parse/grammar.moon`) as data |
| `moonscript/parse/` | the parser; `parser_gen.go` is generated from `internal/grammar` |
| `moonscript/compiler/` | transforms and code generation (`moonscript/transform`, `moonscript/compile`) |
| `moonscript/lint/` | the linter (`moonscript/cmd/lint.moon`) |
| `moonscript/runtime/` | the Lua runtime modules on the fork; `moonscript/runtime/lua` is generated from `moonscript/runtime/moonsrc` |
| `moonscript/testdata/` | MoonScript's reference outputs: specs, parse trees, lint reports, `moon` runs, the corpus manifest |
| `cmd/moonc`, `cmd/moon` | the commands |
| `lua/` | the gopher-lua fork: patched gopher-lua v1.1.2, see `moonscript.patch`, and its own packages (`scope`, `format`, `flow`, `sandbox`) |
| `luascope/`, `luamerge/` | luascope and luamerge (see their CONTRIBUTING.md); luamerge's CLI is `luamerge/cmd/luamerge`, as it uses luamerge's internal packages |
| `internal/luasuites/` | the official Lua 5.1 and LuaJIT test suites on the VM |
| `internal/tools/` | generators, the fuzzer, the benchmark runner and the corpus checker |
| `scripts/` | generators of the reference outputs, the patch and suite checks |

## Generated code

`go generate ./...` regenerates `moonscript/parse/parser_gen.go` (from
`internal/grammar`, by `internal/tools/genparser`) and
`moonscript/runtime/lua` (the upstream `.moon` library sources of
`moonscript/runtime/moonsrc`, compiled by the port). Commit the result with
the change that caused it.

`lua/` has generated files of its own (`vm.go`,
`state.go`, `parse/parser.go`); `moonscript.patch` explains how to
regenerate them. A change there also updates `moonscript.patch`, the full
diff against the original v1.1.2: `scripts/gen-gopher-lua-patch.sh`
regenerates its diff part (edit its header, the list of changes and where
they come from, by hand); CI checks it with `--check`. A fix for Lua
behaviour comes with a script in `lua/testdata/compat`
whose reference output is recorded from real Lua 5.1 (and LuaJIT for
`*.jit.lua`) by `scripts/gen-compat-ref.sh`.

## The reference implementation

The reference outputs in `moonscript/testdata/` come from upstream MoonScript v0.8.0 on
PUC Lua 5.1, in a Docker image. Build it, and unpack the same upstream
sources for the generators that copy upstream files:

```sh
scripts/moonref/build.sh        # image moonref:0.8.0, sources in ~/.cache/go-moonscript/
```

Then regenerate what a change affects:

| Script | Regenerates |
| --- | --- |
| `scripts/gen-parse-golden.sh` | `moonscript/testdata/parse/`: parse trees and parse errors |
| `scripts/gen-docs-golden.sh` | `moonscript/testdata/docs/`: the documentation examples |
| `scripts/gen-upstream-spec-ref.sh` | `moonscript/testdata/upstream_spec/`: upstream's behavioural specs |
| `scripts/gen-lint-golden.sh` | `moonscript/testdata/lint/` |
| `scripts/gen-moon-runner-ref.sh` | `moonscript/testdata/moon_runner/`, `moonscript/testdata/moon_smoke/`: `ref/` from `moon` on Lua 5.1, `ref_luajit/` from `moon` run by `luajit` (image `luajitref:2.1`, built if missing; `lua51` or `luajit` as argument: one of them) |
| `scripts/gen-hook-ref.sh` | `moonscript/runtime/testdata/hooks/` |
| `scripts/gen-compat-ref.sh` | `lua/testdata/compat/ref/`: Lua behaviour tests of the VM |
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
  `moonscript/testdata/bench/moon` on the VM, PUC Lua 5.1 and LuaJIT (Docker), checks
  that every output equals PUC's, and prints the CPU times; `-compare
  old.json` compares with an earlier run.
- `scripts/fuzz-tables.sh` runs the table differential fuzzer with other
  seeds.

## Fuzzing and the corpus

- `go run ./internal/tools/parsefuzz -h`: differential fuzzing of the parser
  against the reference (Docker).
- `go test ./moonscript/parse -fuzz FuzzDifferential`: the generated parser against
  the interpreter it replaced, no Docker needed.
- `internal/tools/corpus`: the real-world corpus check. `fetch.sh` clones
  the MoonScript repositories found on GitHub, `corpus collect` deduplicates
  them and derives error-case mutants, `corpus ref` runs the reference
  (Docker), `corpus run` compares (`corpus -lint` the linter). Run it before
  changes to the parser or the compiler; see the comment at the top of
  [`internal/tools/corpus/main.go`](internal/tools/corpus/main.go).

### The corpus in CI

CI runs the corpus check on every push and pull request (the `corpus`
job), without a local corpus and without the corpus sources in the
repository: they are third-party code under assorted licenses. Committed
instead, in `moonscript/testdata/corpus/`:

- `manifest.tsv` (2.9 MB): for each of the 19,960 distinct files, where to
  fetch it from: a repository at a pinned commit (1,609 repositories, a
  set that covers every file: forks share most of theirs), the path, the
  sha256 and the git blob id;
- `expected.tsv` (2.9 MB): for each file and each of its mutants, digests
  (64-bit sha256 prefixes) of upstream moonc 0.8.0's parse tree, Lua
  output, error messages and lint report (formats in
  [`internal/tools/corpus/expected.go`](internal/tools/corpus/expected.go)).

`scripts/check-corpus.sh` does what CI does, on any machine with Go and
git:

1. `corpus fetch` fetches the files not there yet into
   `$MOON_CORPUS/files` (default `~/.cache/go-moonscript/corpus-pinned`):
   one blob-less fetch of the needed blobs per repository, over
   `GIT_URL` (default `https://github.com/%s.git`; use
   `GIT_URL='git@github.com:%s.git'` for SSH), and checks every sha256.
   Repositories that are gone (deleted, made private, force-pushed and
   garbage-collected) are reported and skipped; it fails only when less
   than 90% of the files are there;
2. `corpus prepare` regenerates the mutants (the generator is seeded by the
   file hash and independent of the Go release; `TestMutantsStable` pins
   it) and the corpus' own `manifest.tsv` and `expected.tsv`;
3. `corpus run -strict` and `corpus -lint -strict` compare with the
   digests and fail on any difference. The report names the files; for a
   diff, make their reference outputs with Docker, `corpus ref -corpus
   $MOON_CORPUS -filter ID` (`ref -lint` for the linter), then `corpus
   -corpus $MOON_CORPUS -diff ID` (`-lint -diff`).

CI caches `files/` (about 20 MB compressed) under the hash of
`manifest.tsv`, so the sources are fetched only when the manifest changes
(then only the new files, from the previous cache) or the cache was
evicted. A warm run takes about 2 minutes, a cold one about 2 more for the
fetch.

After a change of the corpus (`fetch.sh`, `corpus collect`), of the
reference outputs, or of the mutant generator, regenerate both files from
the full local corpus with `scripts/gen-corpus-manifest.sh` (it first makes
the missing reference outputs in Docker; `--no-ref` skips that) and commit
them. It is deterministic: the same corpus gives the same files.

## Commits

- One logical change per commit, with a message in the form
  `prefix: short summary` (`parse: ...`, `compiler: ...`, `runtime: ...`,
  `lua: ...` or `gopher-lua: ...` for the fork, `luascope: ...`,
  `luamerge: ...`, `ci: ...`), and a body that explains why.
- Everything is in English: code, comments, documentation, commit messages.
- User-visible changes go to [`CHANGELOG.md`](CHANGELOG.md) under
  `Unreleased` (luascope's and luamerge's to their own CHANGELOG.md).
