# Lua test suites on our VM

`go test ./internal/luasuites` runs third-party Lua test suites on the VM of
the moon runner (`runtime.NewState`: the patched gopher-lua with the Lua 5.1
compatibility layer and Lua 5.1's stack limits) and compares the outcomes
with the ledger, `expected.tsv`. Every file runs in its own interpreter
process: the test binary re-executes itself as a small `lua` (lua.c's
`-e`, `-l`, `-`, `--`; `luamain_test.go`), with a time limit (120 s) and a 3 GB
address space limit, in a private directory that links to the suite's
files. The same command lines run on the reference interpreters.

| Path | What |
|:---|:---|
| `archives/` | the suites, with provenance and licenses in `archives/SOURCES.md` |
| `lua/run51.lua` | runs one file of lua5.1-tests as its `all.lua` does, prints pass or the first failing assertion |
| `lua/ljrun.lua` | runs one file of LuaJIT-tests (derived from its `test.lua`), prints each test's result |
| `lua/jitstub.lua` | a `jit` table for the LuaJIT view of lua-Harness-tests |
| `lua/parse.lua` | the parse check on the reference interpreters |
| `skip.tsv` | what is not run, with the reasons |
| `ref/lua5.1/`, `ref/luajit/` | the outcomes of PUC Lua 5.1.5 and LuaJIT 2.1 |
| `expected.tsv` | the ledger: our outcomes |

## Suites

- `lua5.1-tests`: lua.org's official Lua 5.1 tests, unchanged. A unit is a
  file, run by `run51.lua` with `all.lua`'s setup (random seed, collector
  parameters, `Message`, the checks of the files' results, `big.lua` in a
  coroutine), each in a fresh interpreter, `_soft` and `_port` unset as in
  `all.lua`. `--setup` is `all.lua`'s setup itself (it asserts
  `os.setlocale"C"`). Not run: `all.lua` (the driver), `checktable.lua` (a
  helper of the internal tests) and `main.lua` (tests lua.c). The parts
  that need the testC library (`T`, a debug build of Lua) skip themselves,
  on every interpreter, as noted by `Message` (the `notes` rows when ours
  differ from lua5.1's). `dofile` is not redirected through `string.dump`
  and `loadstring` as in `all.lua`: binary chunks are not supported.
- `harness`: lua-Harness-tests, TAP tests that pick their assertions by
  `_VERSION`, the `jit` global and a profile. We run as PUC Lua 5.1
  (`-l profile_lua51`, `_VERSION` "Lua 5.1", no `jit`), as lua5.1 runs for
  the reference: that is the target of the VM, and it makes every assertion
  comparable one to one. A row is an assertion whose status (ok, not ok,
  skip, todo) differs from lua5.1's, a run of assertions ours lacks
  (`#a-#b missing`, after a crash), or the file (`-`) when the plan or the
  exit status differ. A `not ok` row shows the first diagnostics.
- `harness-jit`: the LuaJIT view, kept apart: the same files with
  `-l jitstub -l profile_luajit21`, a `jit` table that makes them take
  their LuaJIT 2.1 paths, compared with luajit's run. Its rows are mostly
  LuaJIT extensions we lack (`goto`, `bit`, `table.new`, 5.2 functions of
  LuaJIT 2.1); where 5.1 and LuaJIT disagree, 5.1 wins, so these are not
  bugs as such. Files that test the ffi, the jit.* modules or luajit's
  command line are not run.
- `luajit-tests`: LuaJIT-tests, by `ljrun.lua` with fixed tags: Lua 5.1 with
  the LuaJIT 2.1 extensions (goto, hex floats, bit, table.new), no ffi, no
  JIT compiler, not the slow tests. The index files' entries the tags
  exclude (`trace`, `opt`, `lib/ffi`, ...) are listed as skipped, so are the
  files that need the C test libraries. A row is a test that is not ok on
  our VM, with lua5.1's and luajit's status, or a file that does not load or
  does not complete.
- `tarantool-tests` (in `luajit-tests.tar.gz`) are not run: they need
  Tarantool or a LuaJIT build with its test helpers. They are compiled by
  the parse check.
- `parse`: every `.lua` and `.t` file of the archives (including
  `tarantool`, `vshard` and `crud`, real-world code) must be accepted or
  rejected by `loadstring` as by lua5.1 and by luajit (a first line
  starting with `#` is emptied, as `loadfile` does). A row is a file where
  ours differs from either; LuaJIT-only syntax (`goto`, `0x1p4`, `1LL`,
  `\u{...}`) differs from luajit by design.

## The ledger

`expected.tsv` has a row `<suite> <unit> <item> <outcome>` (tab-separated)
per lua5.1-tests file, and per assertion, test or file that does not match
the reference. A changed, new or missing row fails the test: a regression,
or an improvement whose row must go ("update the ledger"). To accept the
current outcomes:

    LUASUITES_UPDATE=1 go test ./internal/luasuites

(or `go test ./internal/luasuites -update`). The reference outcomes depend
only on the archives and the scripts in `lua/`; to regenerate them too
(Docker, images `moonref:0.8.0` and `luajitref:2.1`):

    scripts/gen-luasuites-ref.sh

The default run (about 10 s) skips the files that take long on our VM
(`slowUnits` in `suites_test.go`: `verybig.lua`, about 110 s);
`LUASUITES_FULL=1` runs them, and the update always does (about 2
minutes). An item whose outcome varies from run to run
(`unstable` in `ledger_test.go`) has a fixed row and is not compared.
