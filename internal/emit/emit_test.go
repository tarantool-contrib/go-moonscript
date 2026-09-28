package emit_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/emit"
	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/luatest"
)

// newModule is a module of the graph at path with source, registered under
// names.
func newModule(path, source string, names ...string) *graph.Module {
	return &graph.Module{ID: 0, FilePath: path, Names: names, Source: []byte(source), Requires: nil}
}

// newGraph is the graph of entry and modules, the entry first, as
// graph.Build makes it.
func newGraph(entry *graph.Module, modules ...*graph.Module) *graph.Graph {
	all := append([]*graph.Module{entry}, modules...)
	byPath := make(map[string]*graph.Module, len(all))

	for i, mod := range all {
		mod.ID = i
		byPath[mod.FilePath] = mod
	}

	return &graph.Graph{Modules: all, ByPath: byPath, Entry: entry, EntryName: "main", Warnings: nil}
}

// defaults are the emit options with everything off.
func defaults() emit.Options {
	return emit.Options{Prefix: "", Suffix: "", Debug: false, Fallback: false, Shebang: "", NoArgFix: false}
}

// emitBundle emits g with opts.
func emitBundle(t *testing.T, g *graph.Graph, opts emit.Options) string {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, emit.Emit(&buf, g, nil, opts))

	return buf.String()
}

// assertRuns runs bundle with args on every interpreter and asserts that it
// exits 0 and prints want. It is what makes a template that emits invalid Lua
// fail these tests, not only the ones that run whole projects.
func assertRuns(t *testing.T, bundle, want string, args ...string) {
	t.Helper()

	opts := luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		r := luatest.RunBundle(t, interp, []byte(bundle), opts, args...)
		assert.Equal(t, 0, r.ExitCode, "the bundle failed\n%s\n--- bundle:\n%s", r, bundle)
		assert.Equal(t, want, r.Stdout, "%s\n--- bundle:\n%s", r, bundle)
	})
}

func TestEmit_RunsTheEntryAsTheMainChunk(t *testing.T) {
	t.Parallel()

	entry := "local m = require(\"m\")\nprint(m.v, select(\"#\", ...), ...)\n"
	g := newGraph(
		newModule("/test/main.lua", entry),
		newModule("/test/m.lua", "return { v = \"module m\" }\n", "m"),
	)

	bundle := emitBundle(t, g, defaults())

	assert.True(t, strings.HasPrefix(bundle, "-- Amalgamated by luamerge\n-- Entry: main\n"), "header\n%s", bundle)
	assert.Contains(t, bundle, "package.preload[\"m\"]", "the module is registered")
	assert.NotContains(t, bundle, "package.preload[\"main\"]", "the entry is not a module")
	assert.True(t, strings.HasSuffix(bundle, "\n"+entry), "the bundle ends with the entry's code\n%s", bundle)
	assertRuns(t, bundle, "module m\t2\tx\ty\n", "x", "y")
}

func TestEmit_RegistersAnEntryRequiredByName(t *testing.T) {
	t.Parallel()

	// A module requires the entry: natively that loads a second instance of
	// the entry's file, which counts its runs here.
	entry := "RUNS = (RUNS or 0) + 1\n" +
		"if select(\"#\", ...) == 0 then print(require(\"m\"), RUNS) end\n" +
		"return \"entry as a module\"\n"
	g := newGraph(
		newModule("/test/main.lua", entry, "main"),
		newModule("/test/m.lua", "return require(\"main\")\n", "m"),
	)

	bundle := emitBundle(t, g, defaults())

	assert.Contains(t, bundle, "package.preload[\"main\"]", "the entry is registered under the name it is required by")
	assertRuns(t, bundle, "entry as a module\t2\n")
}

func TestEmit_RegistersEveryModule(t *testing.T) {
	t.Parallel()

	g := newGraph(
		newModule("/test/main.lua", "require(\"a\")\nrequire(\"b\")\n"),
		newModule("/test/b.lua", "print(\"b\")\n", "b"),
		newModule("/test/a.lua", "print(\"a\")\n", "a"),
	)

	bundle := emitBundle(t, g, defaults())

	assert.Less(t, strings.Index(bundle, "package.preload[\"a\"]"), strings.Index(bundle, "package.preload[\"b\"]"),
		"modules are emitted sorted by name, so the output does not depend on discovery order")
	assertRuns(t, bundle, "a\nb\n")
}

func TestEmit_ModuleWithAliases(t *testing.T) {
	t.Parallel()

	g := newGraph(
		newModule("/test/main.lua", "print(require(\"foo\") == require(\"foo.bar\"), COUNT)\n"),
		newModule("/test/foo.lua", "COUNT = (COUNT or 0) + 1\nreturn {}\n", "foo", "foo.bar"),
	)

	bundle := emitBundle(t, g, defaults())

	// The primary name carries the real loader; aliases delegate to it.
	assert.Contains(t, bundle, "package.preload[\"foo\"] = function(...)")
	assert.Contains(t, bundle, "package.preload[\"foo.bar\"] = function(...) return require(\"foo\") end")
	assertRuns(t, bundle, "true\t1\n")
}

func TestEmit_Suffix(t *testing.T) {
	t.Parallel()

	g := newGraph(newModule("/test/main.lua", "print(\"hello\")\n"))
	opts := defaults()
	opts.Prefix = "print(\"prefix\")"
	opts.Suffix = "print(\"suffix\")"

	assertRuns(t, emitBundle(t, g, opts), "prefix\nhello\nsuffix\n")
}

func TestEmit_Fallback(t *testing.T) {
	t.Parallel()

	g := newGraph(
		newModule("/test/main.lua", "print((require(\"m\")))\n"),
		newModule("/test/m.lua", "return \"embedded m\"\n", "m"),
	)
	opts := defaults()
	opts.Fallback = true

	bundle := emitBundle(t, g, opts)

	// Fallback registers in package.postload behind an appended searcher, not
	// in package.preload.
	assert.Contains(t, bundle, "package.postload[\"m\"]", "fallback should register in package.postload")
	assert.Contains(t, bundle, "searchers[#searchers+1] = function(mod)", "fallback should append a searcher")
	assert.NotContains(t, bundle, "package.preload[", "fallback should not use package.preload")
	assertRuns(t, bundle, "embedded m\n")
}

func TestEmit_Debug(t *testing.T) {
	t.Parallel()

	g := newGraph(
		newModule("/test/main.lua", "print(require(\"m\"), ...)\n"),
		newModule("/test/m.lua", "return \"module m\"\n", "m"),
	)
	opts := defaults()
	opts.Debug = true

	bundle := emitBundle(t, g, opts)

	assert.Contains(t, bundle, "\"@/test/m.lua\"", "a module is loaded under its own chunk name")
	assert.Contains(t, bundle, "\"@/test/main.lua\"", "so is the entry")
	assertRuns(t, bundle, "module m\tx\n", "x")
}

func TestEmit_Shebang(t *testing.T) {
	t.Parallel()

	opts := defaults()
	opts.Shebang = "#!/usr/bin/env lua"

	bundle := emitBundle(t, newGraph(newModule("/test/main.lua", "print(\"hello\")\n")), opts)

	// The shebang must be the very first line of the file.
	assert.True(t, strings.HasPrefix(bundle, "#!/usr/bin/env lua\n"), "the shebang must be the first line:\n%s", bundle)
	assertRuns(t, bundle, "hello\n")
}

func TestEmit_NoArgFix(t *testing.T) {
	t.Parallel()

	g := newGraph(
		newModule("/test/main.lua", "require(\"m\")\n"),
		newModule("/test/m.lua", "print(\"m\")\n", "m"),
	)
	off := defaults()
	off.NoArgFix = true

	assert.Contains(t, emitBundle(t, g, defaults()), "local arg = _G.arg", "arg alias should be present by default")
	assert.NotContains(t, emitBundle(t, g, off), "local arg = _G.arg", "NoArgFix should omit the arg alias")
}

func TestEmit_PreservesMultilineString(t *testing.T) {
	t.Parallel()

	// A module whose body contains a multi-line [[ ... ]] string literal with
	// internal indentation. Emitting must not reindent the source, or the
	// string's contents would change.
	g := newGraph(
		newModule("/test/main.lua", "io.write((require(\"data\")))\n"),
		newModule("/test/data.lua", "return [[\nalpha\n  beta\ngamma\n]]\n", "data"),
	)

	assertRuns(t, emitBundle(t, g, defaults()), "alpha\n  beta\ngamma\n")
}

func TestEmit_RefusesAMalformedGraph(t *testing.T) {
	t.Parallel()

	noEntry := newGraph(newModule("/test/main.lua", "print(1)\n"))
	noEntry.Entry = nil

	unnamed := newGraph(newModule("/test/main.lua", "print(1)\n"), newModule("/test/m.lua", "return 1\n"))

	for name, g := range map[string]*graph.Graph{"no entry": noEntry, "a module without a name": unnamed} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			assert.Error(t, emit.Emit(&buf, g, nil, defaults()))
		})
	}
}
