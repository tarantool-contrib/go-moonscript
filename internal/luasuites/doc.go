// Package luasuites runs third-party Lua test suites on the Lua VM of the
// moon runner (the patched gopher-lua in third_party/gopher-lua, set up by
// runtime.NewState) and pins their outcomes. It has no API; see
// the tests (Linux only) and testdata/README.md.
//
// The suites, in testdata/archives (provenance and licenses in
// testdata/archives/SOURCES.md):
//
//   - lua5.1-tests: the official Lua 5.1 test suite, unchanged; every file
//     runs as its all.lua runs it (testdata/lua/run51.lua) and its outcome
//     is pass or the first failing assertion;
//   - lua-Harness-tests: TAP tests, compared assertion by assertion with
//     PUC Lua 5.1.5 (and, with the VM in its LuaJIT dialect, with LuaJIT
//     2.1);
//   - LuaJIT-tests: run without the ffi and the JIT compiler
//     (testdata/lua/ljrun.lua) on the VM in its LuaJIT dialect, compared
//     test by test with LuaJIT 2.1 and PUC Lua 5.1.5;
//   - parse: every Lua file of the archives must compile, or not, as with
//     lua5.1 and luajit.
//
// The expected outcomes are in testdata/expected.tsv, the outcomes of the
// reference interpreters in testdata/ref (scripts/gen-luasuites-ref.sh).
package luasuites
