package emit_test

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gopherparse "github.com/tarantool-contrib/go-moonscript/lua/parse"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/emit"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/graph"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/luatest"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/transform"
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

// emitBundle emits g with opts and no transform; see emitTransformed.
func emitBundle(t *testing.T, g *graph.Graph, opts emit.Options) string {
	t.Helper()

	return emitTransformed(t, g, nil, opts)
}

// emitTransformed emits g with transforms and opts and asserts that
// gopher-lua parses the bundle. The bundle's own code is Lua 5.1, which every
// interpreter reads, and so is the code of every fixture here, so a template
// that writes invalid Lua fails these tests even where no interpreter is
// installed to run the bundle. gopher-lua knows no header: it gets what
// follows a shebang, as the interpreter does.
func emitTransformed(t *testing.T, g *graph.Graph, transforms []transform.Transformer, opts emit.Options) string {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, emit.Emit(&buf, g, transforms, opts))

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
// under csv. The other names of the module are registered as usual. The
// embedded module gets the arguments require passes to a loader, its name
// first, through either loader.
func TestEmit_DefersPrefixedBuiltinNames(t *testing.T) {
	t.Parallel()

	csv := newModule("/test/csv.lua", "return { name = (...) }\n", "p.csv", "p.compat.csv")
	csv.Deferrals = []graph.Deferral{{Name: "p.csv", Builtin: "csv"}}
	g := newGraph(newModule("/test/main.lua",
		"print(require(\"p.csv\").name or \"built-in\", require(\"p.compat.csv\").name)\n"), csv)

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
					"csv.lua":    "return { name = \"a csv.lua on disk\" }\n",
				}), opts, "bundle.lua")

				want := "p.csv"
				if probe.Stdout == "built-in" {
					want = probe.Stdout
				}

				assert.Equal(t, 0, r.ExitCode, "the bundle failed\n%s\n--- bundle:\n%s", r, bundle)
				assert.Equal(t, want+"\tp.compat.csv\n", r.Stdout, "%s\n--- bundle:\n%s", r, bundle)
			})
		})
	}
}

func TestEmit_Suffix(t *testing.T) {
	t.Parallel()

	// return must be the last statement of the entry's block, and the suffix
	// still runs after it. The entry, a function then, gets the bundle's
	// `...`, and it reads the global arg (arg[0] is the script, and arg.n is
	// not set), which on PUC Lua 5.1 the implicit arg local of a vararg
	// function hides unless the arg line fills it.
	g := newGraph(newModule("/test/main.lua",
		"print(\"hello\", select(\"#\", ...), ...)\nprint(type(arg[0]), arg.n)\nreturn \"result\"\n"))

	for name, debug := range map[string]bool{"default": false, "debug": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Prefix = "print(\"prefix\")"
			opts.Suffix = "print(\"suffix\")"
			opts.Debug = debug

			assertRuns(t, emitBundle(t, g, opts), "prefix\nhello\t2\tx\ty\nstring\tnil\nsuffix\n", "x", "y")
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
// the appended searcher, as before. The module has two such names, and the
// wrapper serves both.
func TestEmit_ServesBuiltinNamesAfterTheFirstSearcher(t *testing.T) {
	t.Parallel()

	csv := newModule("/test/csv.lua", "return { impl = \"embedded\" }\n", "csv", "json", "compat.csv")
	csv.BuiltinNames = []string{"csv", "json"}
	g := newGraph(newModule("/test/main.lua", "print(require(\"csv\").impl or \"built-in\", "+
		"require(\"json\").impl or \"built-in\", require(\"compat.csv\").impl)\n"), csv)
	wrapper := "__luamerge_searchers[1] = function(name, ...)\n" +
		"  if name ~= \"csv\" and name ~= \"json\" then return __luamerge_first(name, ...) end\n"

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
				"package.postload[\"csv\"] = __luamerge_loader", "package.postload[\"json\"] = __luamerge_loader",
				"package.postload[\"compat.csv\"] = __luamerge_loader", "searchers[#searchers+1] = function(mod)",
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

				// Whether the interpreter has a csv and a json module of its own:
				// a probe run where no csv.lua or json.lua is on package.path.
				opts := luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}
				dir := luatest.WriteProject(t, luatest.Project{
					"probe.lua": "io.write(pcall(require, \"csv\") and \"built-in\" or \"embedded\", \"\\t\",\n" +
						"  pcall(require, \"json\") and \"built-in\" or \"embedded\")\n",
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

// TestEmit_Debug: in debug mode a module and the entry are loaded as chunks
// of their own, named as the interpreter names their files when the program
// runs from the root, with a suffix too, and the entry's chunk gets the
// bundle's `...`. Each chunk prints the name it runs under.
func TestEmit_Debug(t *testing.T) {
	t.Parallel()

	g := newGraph(
		newModule("/test/main.lua", "print(require(\"m\"), debug.getinfo(1, \"S\").source, ...)\n"),
		newModule("/test/m.lua", "return debug.getinfo(1, \"S\").source\n", "m"),
	)

	for name, suffix := range map[string]string{"without a suffix": "", "with a suffix": "print(\"suffix\")"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Debug = true
			opts.Suffix = suffix

			bundle := emitBundle(t, g, opts)

			assert.Contains(t, bundle, "\"@./m.lua\"", "a module is loaded under its own chunk name")
			assert.Contains(t, bundle, "\"@main.lua\"", "so is the entry")
			assert.NotContains(t, bundle, "/test", "no chunk name holds the root")

			want := "@./m.lua\t@main.lua\tx\n"
			if suffix != "" {
				want += "suffix\n"
			}

			assertRuns(t, bundle, want, "x")
		})
	}
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

// TestEmit_Shebang: the shebang is the very first line of the bundle and the
// header the line after it, whether the shebang comes with a line break of its
// own, as a YAML block scalar gives it, or without.
func TestEmit_Shebang(t *testing.T) {
	t.Parallel()

	for name, shebang := range map[string]string{
		"without a line break": "#!/usr/bin/env lua",
		"with a line break":    "#!/usr/bin/env lua\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Shebang = shebang

			bundle := emitBundle(t, newGraph(newModule("/test/main.lua", "print(\"hello\")\n")), opts)

			assert.True(t, strings.HasPrefix(bundle, "#!/usr/bin/env lua\n-- Amalgamated by luamerge\n"),
				"the shebang must be the first line and the header the next:\n%s", bundle)
			assertRuns(t, bundle, "hello\n")
		})
	}
}

// TestEmit_NoArgFix: the arg line is in front of the code of every module
// and, with a suffix, which makes the entry a function, in front of the
// entry's code too; NoArgFix drops it from both. The line runs on PUC Lua 5.1
// only: what it does is tested by running bundles there.
func TestEmit_NoArgFix(t *testing.T) {
	t.Parallel()

	g := newGraph(
		newModule("/test/main.lua", "require(\"m\")\n"),
		newModule("/test/m.lua", "print(\"m\")\n", "m"),
	)
	line := "if not rawget(_G, \"jit\") then arg = rawget(_G, \"arg\") end\n"

	for name, tt := range map[string]struct {
		suffix string
		// lines is the number of arg lines in the bundle by default.
		lines int
		want  string
	}{
		"without a suffix": {suffix: "", lines: 1, want: "m\n"},
		"with a suffix":    {suffix: "print(\"suffix\")", lines: 2, want: "m\nsuffix\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			on := defaults()
			on.Suffix = tt.suffix
			off := on
			off.NoArgFix = true

			assert.Equal(t, tt.lines, strings.Count(emitBundle(t, g, on), line), "the arg lines by default")

			bundle := emitBundle(t, g, off)
			assert.NotContains(t, bundle, "arg = rawget", "NoArgFix drops every arg line")
			assertRuns(t, bundle, tt.want)
		})
	}
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

// testAppend is a transform that appends its text to the source.
type testAppend string

func (text testAppend) Transform(source []byte) ([]byte, error) {
	return append(slices.Clone(source), text...), nil
}

// errTestTransform is the error of testFailOn.
var errTestTransform = errors.New("the transform failed")

// testFailOn is a transform that fails on a source holding its text and
// leaves any other source as it is.
type testFailOn string

func (text testFailOn) Transform(source []byte) ([]byte, error) {
	if bytes.Contains(source, []byte(text)) {
		return nil, errTestTransform
	}

	return source, nil
}

// TestEmit_AppliesTransformsInOrder: the transforms apply to the code of the
// entry and of every module, one after another in the order given, each to
// what the one before it made.
func TestEmit_AppliesTransformsInOrder(t *testing.T) {
	t.Parallel()

	g := newGraph(
		newModule("/test/main.lua", "require(\"m\")\n"),
		newModule("/test/m.lua", "print(\"m\")\n", "m"),
	)
	transforms := []transform.Transformer{testAppend("print(\"first\")\n"), testAppend("print(\"second\")\n")}

	for name, debug := range map[string]bool{"default": false, "debug": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Debug = debug

			bundle := emitTransformed(t, g, transforms, opts)

			for _, source := range []string{"require(\"m\")\n", "print(\"m\")\n"} {
				assert.Contains(t, bundle, source+"print(\"first\")\nprint(\"second\")\n", "the code as transformed")
			}

			assertRuns(t, bundle, "m\nfirst\nsecond\nfirst\nsecond\n")
		})
	}
}

// TestEmit_RefusesAFailedTransform: when a transform fails on the code of a
// module, Emit fails naming the module's file and writes nothing, rather than
// embed the module without its code.
func TestEmit_RefusesAFailedTransform(t *testing.T) {
	t.Parallel()

	g := newGraph(
		newModule("/test/main.lua", "require(\"m\")\n"),
		newModule("/test/m.lua", "return \"boom\"\n", "m"),
	)

	var buf bytes.Buffer

	err := emit.Emit(&buf, g, []transform.Transformer{testFailOn("boom")}, defaults())

	require.ErrorIs(t, err, errTestTransform)
	assert.Contains(t, err.Error(), "/test/m.lua", "the error names the module's file")
	assert.Zero(t, buf.Len(), "nothing is written")
}

// TestEmit_KeepsLoadersRegisteredFirst: a loader registered before the bundle
// runs, in package.preload or, in fallback mode, in package.postload, wins
// over the embedded module, as a loader in package.preload wins over a file
// natively: under a plain name and under a name that a package prefix made of
// the name of a runtime's built-in module alike.
func TestEmit_KeepsLoadersRegisteredFirst(t *testing.T) {
	t.Parallel()

	csv := newModule("/test/csv.lua", "return \"embedded p.csv\"\n", "p.csv")
	csv.Deferrals = []graph.Deferral{{Name: "p.csv", Builtin: "csv"}}
	g := newGraph(newModule("/test/main.lua", "print((require(\"p.m\")), (require(\"p.csv\")))\n"),
		newModule("/test/m.lua", "return \"embedded p.m\"\n", "p.m"), csv)

	for name, tt := range map[string]struct {
		debug, fallback bool
		registry        string
	}{
		"default":  {debug: false, fallback: false, registry: "package.preload"},
		"debug":    {debug: true, fallback: false, registry: "package.preload"},
		"fallback": {debug: false, fallback: true, registry: "package.postload"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Debug, opts.Fallback = tt.debug, tt.fallback

			bundle := emitBundle(t, g, opts)
			host := tt.registry + " = " + tt.registry + " or {}\n" +
				tt.registry + "[\"p.m\"] = function() return \"registered p.m\" end\n" +
				tt.registry + "[\"p.csv\"] = function() return \"registered p.csv\" end\n" +
				"dofile(\"bundle.lua\")\n"

			luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
				t.Helper()

				dir := luatest.WriteProject(t, luatest.Project{"host.lua": host, "bundle.lua": bundle})
				r := luatest.Run(t, interp, dir, luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}, "host.lua")
				assert.Equal(t, 0, r.ExitCode, "%s\n--- bundle:\n%s", r, bundle)
				assert.Equal(t, "registered p.m\tregistered p.csv\n", r.Stdout, "%s\n--- bundle:\n%s", r, bundle)
			})
		})
	}
}

// TestEmit_ModuleEnvironmentStaysItsOwn: a module that replaces its
// environment replaces only its own, as a module file does natively. module()
// sets the environment of the function that calls it (Lua 5.1, LuaJIT,
// Tarantool), which is the function around the module's code, not the bundle's
// chunk, so every global the entry reads after the require still comes from
// the global table.
func TestEmit_ModuleEnvironmentStaysItsOwn(t *testing.T) {
	t.Parallel()

	g := newGraph(
		newModule("/test/main.lua", "require(\"m\")\nprint(rawget(_G, \"x\") == x)\n"),
		newModule("/test/m.lua", "if module then module(\"m\", package.seeall) end\nx = \"set by m\"\n", "m"),
	)

	for name, debug := range map[string]bool{"default": false, "debug": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Debug = debug

			assertRuns(t, emitBundle(t, g, opts), "true\n")
		})
	}
}

// TestEmit_SuffixHasABlockOfItsOwn: the suffix runs in a block of its own, as
// the README says: it may end with a return statement, which must be the last
// statement of a block, and its locals do not reach the code after it, which
// returns the entry's results.
func TestEmit_SuffixHasABlockOfItsOwn(t *testing.T) {
	t.Parallel()

	g := newGraph(newModule("/test/main.lua", "print(\"main\")\n"))

	for name, suffix := range map[string]string{
		"a return statement":                    "print(\"suffix\")\nreturn",
		"locals of names the bundle then reads": "local table, unpack = nil, nil\nprint(\"suffix\")",
	} {
		for mode, debug := range map[string]bool{"default": false, "debug": true} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				t.Parallel()

				opts := defaults()
				opts.Suffix = suffix
				opts.Debug = debug

				assertRuns(t, emitBundle(t, g, opts), "main\nsuffix\n")
			})
		}
	}
}

// TestEmit_BuiltinNameKeepsTheLoaderData: where the interpreter's first
// searcher finds a module under a name some runtime has built in, here a
// loader the host put in package.preload, the bundle hands on what the
// searcher found, the loader and its loader data, as require gets them
// natively. The original runs next to the files of the project, the bundle
// next to nothing.
func TestEmit_BuiltinNameKeepsTheLoaderData(t *testing.T) {
	t.Parallel()

	project := luatest.Project{
		"main.lua": "local function show(...) print(\"require returns\", select(\"#\", ...), ...) end\n" +
			"show(require(\"csv\"))\n",
		"csv.lua": "return \"the project's csv\"\n",
	}
	csv := newModule("/test/csv.lua", project["csv.lua"], "csv")
	csv.BuiltinNames = []string{"csv"}
	g := newGraph(newModule("/test/main.lua", project["main.lua"]), csv)
	host := "package.preload[\"csv\"] = function(...)\n" +
		"  print(\"the loader gets\", select(\"#\", ...), ...)\n" +
		"  return \"the host's csv\"\n" +
		"end\n" +
		"dofile(arg[1])\n"
	runOpts := luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}

	for name, debug := range map[string]bool{"default": false, "debug": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Debug = debug
			bundle := emitBundle(t, g, opts)

			luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
				t.Helper()

				original := maps.Clone(project)
				original["host.lua"] = host
				orig := luatest.Run(t, interp, luatest.WriteProject(t, original), runOpts, "host.lua", "main.lua")
				bun := luatest.Run(t, interp, luatest.WriteProject(t, luatest.Project{
					"host.lua": host, "bundle.lua": bundle,
				}), runOpts, "host.lua", "bundle.lua")

				require.Equal(t, 0, orig.ExitCode, "the original\n%s", orig)
				require.Contains(t, orig.Stdout, "the host's csv", "the original\n%s", orig)
				luatest.AssertSame(t, orig, bun)
			})
		})
	}
}

// TestEmit_ManyModules: a bundle holds any number of modules. The loader of
// each module and the locals around it are locals of a block of the module's
// own: in the bundle's chunk they would add up, and a chunk holds 200 locals
// at most.
func TestEmit_ManyModules(t *testing.T) {
	t.Parallel()

	const count = 250

	modules := make([]*graph.Module, 0, count)
	for i := 1; i <= count; i++ {
		modules = append(modules,
			newModule(fmt.Sprintf("/test/m%d.lua", i), fmt.Sprintf("return %d\n", i), fmt.Sprintf("m%d", i)))
	}

	g := newGraph(newModule("/test/main.lua",
		fmt.Sprintf("local sum = 0\nfor i = 1, %d do sum = sum + require(\"m\" .. i) end\nprint(sum)\n", count)),
		modules...)

	for name, debug := range map[string]bool{"default": false, "debug": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Debug = debug

			assertRuns(t, emitBundle(t, g, opts), fmt.Sprintf("%d\n", count*(count+1)/2))
		})
	}
}

// TestEmit_PrefixRunsFirst: the prefix runs before any module is registered,
// as the README says, so what it sets up is in place when they are; here it
// finds that nothing is registered yet.
func TestEmit_PrefixRunsFirst(t *testing.T) {
	t.Parallel()

	g := newGraph(
		newModule("/test/main.lua", "print((require(\"m\")))\n"),
		newModule("/test/m.lua", "return \"m\"\n", "m"),
	)
	prefix := "local registry = package.postload or package.preload\n" +
		"print(registry[\"m\"] == nil and \"nothing is registered yet\" or \"m is registered already\")"

	for name, tt := range map[string]struct{ debug, fallback bool }{
		"default":  {debug: false, fallback: false},
		"debug":    {debug: true, fallback: false},
		"fallback": {debug: false, fallback: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := defaults()
			opts.Prefix = prefix
			opts.Debug, opts.Fallback = tt.debug, tt.fallback

			assertRuns(t, emitBundle(t, g, opts), "nothing is registered yet\nm\n")
		})
	}
}
