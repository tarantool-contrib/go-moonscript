package emit_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gopherparse "github.com/yuin/gopher-lua/parse"

	"github.com/tarantool/go-luamerge/internal/emit"
	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/lex"
	"github.com/tarantool/go-luamerge/internal/luatest"
)

// newModule is a module of the graph at path with source, registered under
// names.
func newModule(path, source string, names ...string) *graph.Module {
	return &graph.Module{
		ID: 0, FilePath: path, Names: names, BuiltinNames: nil, Deferrals: nil, Source: []byte(source), Requires: nil,
	}
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

// defaults are the emit options with everything off, for a project in /test
// with no search directory.
func defaults() emit.Options {
	return emit.Options{
		Root: "/test", SearchDirs: nil, Prefix: "", Suffix: "", Debug: false, Fallback: false, Shebang: "",
		NoArgFix: false,
	}
}

// emitBundle emits g with opts and asserts that gopher-lua parses the bundle.
// The bundle's own code is Lua 5.1, which every interpreter reads, and so is
// the code of every fixture here, so a template that writes invalid Lua fails
// these tests even where no interpreter is installed to run the bundle.
// gopher-lua knows no header: it gets what follows a shebang, as the
// interpreter does.
func emitBundle(t *testing.T, g *graph.Graph, opts emit.Options) string {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, emit.Emit(&buf, g, nil, opts))

	bundle := buf.Bytes()
	_, err := gopherparse.Parse(bytes.NewReader(bundle[lex.Header(bundle):]), "bundle.lua")
	require.NoError(t, err, "gopher-lua cannot parse the bundle\n%s", bundle)

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

func TestEmit_RegistersEveryName(t *testing.T) {
	t.Parallel()

	// Two names of one file are two modules, as natively: the body runs once
	// per name and sees its own name.
	g := newGraph(
		newModule("/test/main.lua", "local a, b = require(\"foo\"), require(\"foo.bar\")\n"+
			"print(a == b, COUNT, a.name, b.name)\n"),
		newModule("/test/foo.lua", "COUNT = (COUNT or 0) + 1\nreturn { name = (...) }\n", "foo", "foo.bar"),
	)

	for name, debug := range map[string]bool{"default": false, "debug": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Debug = debug

			assertRuns(t, emitBundle(t, g, opts), "false\t2\tfoo\tfoo.bar\n")
		})
	}
}

// TestEmit_DefersPrefixedBuiltinNames: a name that a package prefix made of
// the name of a module some runtime has built in, p.csv of csv, is registered
// with a loader that gives the runtime's own module where the runtime has one
// and the embedded module elsewhere, as the require of csv it stands for
// does. The runtime's module is looked for in package.loaded and by the
// interpreter's first searcher only, never on disk, and nothing is registered
// under csv. The other names of the module are registered as usual.
func TestEmit_DefersPrefixedBuiltinNames(t *testing.T) {
	t.Parallel()

	csv := newModule("/test/csv.lua", "return { impl = \"embedded\" }\n", "p.csv", "p.compat.csv")
	csv.Deferrals = []graph.Deferral{{Name: "p.csv", Builtin: "csv"}}
	g := newGraph(newModule("/test/main.lua",
		"print(require(\"p.csv\").impl or \"built-in\", require(\"p.compat.csv\").impl)\n"), csv)

	for name, tt := range map[string]struct {
		debug, fallback bool
		registry        string
	}{
		"default":  {debug: false, fallback: false, registry: "preload"},
		"debug":    {debug: true, fallback: false, registry: "preload"},
		"fallback": {debug: false, fallback: true, registry: "postload"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Debug, opts.Fallback = tt.debug, tt.fallback

			bundle := emitBundle(t, g, opts)

			assert.Contains(t, bundle, "package."+tt.registry+"[\"p.csv\"] = function(...)\n")
			assert.Contains(t, bundle, "package."+tt.registry+"[\"p.compat.csv\"] = __luamerge_loader end\n")
			assert.NotContains(t, bundle, "package."+tt.registry+"[\"p.csv\"] = __luamerge_loader")
			assert.NotContains(t, bundle, "package."+tt.registry+"[\"csv\"] = ", "nothing is registered as csv")
			assert.NotContains(t, bundle, "__luamerge_searchers", "no searcher serves csv")

			luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
				t.Helper()

				// Whether the interpreter has a csv module of its own. The
				// bundle runs next to a csv.lua, which it must not take for
				// one.
				opts := luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}
				dir := luatest.WriteProject(t, luatest.Project{
					"probe.lua": "io.write(pcall(require, \"csv\") and \"built-in\" or \"embedded\")\n",
				})
				probe := luatest.Run(t, interp, dir, opts, "probe.lua")
				require.Equal(t, 0, probe.ExitCode, "the probe\n%s", probe)

				r := luatest.Run(t, interp, luatest.WriteProject(t, luatest.Project{
					"bundle.lua": bundle,
					"csv.lua":    "return { impl = \"a csv.lua on disk\" }\n",
				}), opts, "bundle.lua")
				assert.Equal(t, 0, r.ExitCode, "the bundle failed\n%s\n--- bundle:\n%s", r, bundle)
				assert.Equal(t, probe.Stdout+"\tembedded\n", r.Stdout, "%s\n--- bundle:\n%s", r, bundle)
			})
		})
	}
}

func TestEmit_Suffix(t *testing.T) {
	t.Parallel()

	// return must be the last statement of the entry's block, and the suffix
	// still runs after it.
	g := newGraph(newModule("/test/main.lua", "print(\"hello\")\nreturn \"result\"\n"))

	for name, debug := range map[string]bool{"default": false, "debug": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Prefix = "print(\"prefix\")"
			opts.Suffix = "print(\"suffix\")"
			opts.Debug = debug

			assertRuns(t, emitBundle(t, g, opts), "prefix\nhello\nsuffix\n")
		})
	}
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

// TestEmit_ServesBuiltinNamesAfterTheFirstSearcher: a name that some runtime
// has a built-in module of is served by a wrapper around the interpreter's
// first searcher, which hands out the embedded loader only when that
// searcher finds nothing, so the runtime's module wins where there is one
// and the embedded one is used elsewhere. The other names of the module stay
// in package.preload. Tarantool finds its csv through its first searcher,
// after package.preload, so there a loader in package.preload would win over
// the built-in. In fallback mode every name goes to package.postload, behind
// the appended searcher, as before.
func TestEmit_ServesBuiltinNamesAfterTheFirstSearcher(t *testing.T) {
	t.Parallel()

	csv := newModule("/test/csv.lua", "return { impl = \"embedded\" }\n", "csv", "compat.csv")
	csv.BuiltinNames = []string{"csv"}
	g := newGraph(newModule("/test/main.lua",
		"print(require(\"csv\").impl or \"built-in\", require(\"compat.csv\").impl)\n"), csv)
	wrapper := "__luamerge_searchers[1] = function(name, ...)\n" +
		"  if name ~= \"csv\" then return __luamerge_first(name, ...) end\n"

	for name, tt := range map[string]struct {
		debug, fallback bool
		want, notWant   []string
	}{
		"default": {
			debug: false, fallback: false,
			want:    []string{wrapper, "package.preload[\"compat.csv\"] = __luamerge_loader"},
			notWant: []string{"package.preload[\"csv\"]", "postload"},
		},
		"debug": {
			debug: true, fallback: false,
			want:    []string{wrapper, "package.preload[\"compat.csv\"] = __luamerge_loader"},
			notWant: []string{"package.preload[\"csv\"]", "postload"},
		},
		"fallback": {
			debug: false, fallback: true,
			want: []string{
				"package.postload[\"csv\"] = __luamerge_loader", "package.postload[\"compat.csv\"] = __luamerge_loader",
				"searchers[#searchers+1] = function(mod)",
			},
			notWant: []string{"__luamerge_first", "package.preload["},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Debug, opts.Fallback = tt.debug, tt.fallback

			bundle := emitBundle(t, g, opts)

			for _, want := range tt.want {
				assert.Contains(t, bundle, want)
			}

			for _, notWant := range tt.notWant {
				assert.NotContains(t, bundle, notWant)
			}

			luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
				t.Helper()

				// Whether the interpreter has a csv module of its own: a probe
				// run where no csv.lua is on package.path.
				opts := luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}
				dir := luatest.WriteProject(t, luatest.Project{
					"probe.lua": "io.write(pcall(require, \"csv\") and \"built-in\" or \"embedded\")\n",
				})
				probe := luatest.Run(t, interp, dir, opts, "probe.lua")
				require.Equal(t, 0, probe.ExitCode, "the probe\n%s", probe)

				r := luatest.RunBundle(t, interp, []byte(bundle), opts)
				assert.Equal(t, 0, r.ExitCode, "the bundle failed\n%s\n--- bundle:\n%s", r, bundle)
				assert.Equal(t, probe.Stdout+"\tembedded\n", r.Stdout, "%s\n--- bundle:\n%s", r, bundle)
			})
		})
	}
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

	assert.Contains(t, bundle, "\"@./m.lua\"", "a module is loaded under its own chunk name")
	assert.Contains(t, bundle, "\"@main.lua\"", "so is the entry")
	assert.NotContains(t, bundle, "/test", "no chunk name holds the root")
	assertRuns(t, bundle, "module m\tx\n", "x")
}

// TestEmit_RefusesRelativeDirectories: the chunk names of a debug bundle are
// paths from the root, found by comparing the files' absolute paths with
// the root and the search directories, which a relative one cannot be
// compared with.
func TestEmit_RefusesRelativeDirectories(t *testing.T) {
	t.Parallel()

	g := newGraph(newModule("/test/main.lua", "print(1)\n"))

	for name, tt := range map[string]struct {
		root       string
		searchDirs []string
	}{
		"no root":                     {root: "", searchDirs: nil},
		"a relative root":             {root: "test", searchDirs: nil},
		"a relative search directory": {root: "/test", searchDirs: []string{"/vendor", "lib"}},
	} {
		opts := defaults()
		opts.Root, opts.SearchDirs = tt.root, tt.searchDirs

		var buf bytes.Buffer
		assert.Error(t, emit.Emit(&buf, g, nil, opts), name)
	}
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

	// The line runs on PUC Lua 5.1 only: what it does is tested by running
	// bundles there, what is checked here is that NoArgFix drops it.
	line := "if _VERSION == \"Lua 5.1\" and not rawget(_G, \"jit\") then arg = rawget(_G, \"arg\") end\n"
	assert.Contains(t, emitBundle(t, g, defaults()), line, "the arg line is present by default")
	assert.NotContains(t, emitBundle(t, g, off), "arg = rawget", "NoArgFix drops the arg line")
	assertRuns(t, emitBundle(t, g, off), "m\n")
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
