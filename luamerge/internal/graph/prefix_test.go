package graph_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/config"
	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/parse"
	"github.com/tarantool/go-luamerge/internal/resolve"
)

// testPrefixBuild writes files, by slash-separated path, into a fresh project
// directory and builds its graph from main.lua in strict mode with the
// package prefix prefix, after tweak, when not nil, has changed the
// configuration. It returns the graph, the directory, whose symbolic links
// are resolved as Build resolves the entry's, and Build's error.
func testPrefixBuild(
	t *testing.T, files map[string]string, prefix string, tweak func(cfg *config.Config),
) (*graph.Graph, string, error) {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	cfg := entryConfig(dir, "main.lua")
	cfg.PackagePrefix = prefix

	if tweak != nil {
		tweak(&cfg)
	}

	resolver := resolve.NewWithPrefix(cfg.Root, nil, cfg.Path, cfg.StripPrefix)
	g, err := graph.Build(&cfg, parse.New(), resolver)

	return g, dir, err
}

// testNamesByFile returns the names of every module of g by its file,
// relative to dir and slash-separated.
func testNamesByFile(t *testing.T, g *graph.Graph, dir string) map[string][]string {
	t.Helper()

	names := make(map[string][]string)

	for _, mod := range g.Modules {
		rel, err := filepath.Rel(dir, mod.FilePath)
		require.NoError(t, err)

		names[filepath.ToSlash(rel)] = mod.Names
	}

	return names
}

// TestBuild_PackagePrefixNames: under a package prefix a module is
// registered under the prefixed form of each of its names only, a name that
// carries the prefix already staying as it is. A name written as a path is a
// name like any other and gets the prefix and a dot in front, which
// package.searchpath finds under the prefix's directory as it finds the name
// itself under the root. Names that come out the same are one. The entry has
// no name while nothing requires it.
func TestBuild_PackagePrefixNames(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files map[string]string
		tweak func(cfg *config.Config)
		want  map[string][]string
	}{
		"package prefix": {
			files: map[string]string{
				"main.lua": "require(\"x\")\nrequire(\"p.z\")\nrequire(\"./x\")\nrequire(\"a/b\")\n",
				"x.lua":    "return 1\n",
				"p/z.lua":  "return 2\n",
				"a/b.lua":  "return 3\n",
				"inc.lua":  "return 4\n",
			},
			tweak: func(cfg *config.Config) { cfg.IncludePackages = []string{"inc"} },
			want: map[string][]string{
				"main.lua": nil,
				"x.lua":    {"p.x", "p../x"},
				"p/z.lua":  {"p.z"},
				"a/b.lua":  {"p.a/b"},
				"inc.lua":  {"p.inc"},
			},
		},
		"package name": {
			files: map[string]string{
				"main.lua":  "require(\"utils\")\nrequire(\"p.utils\")\nrequire(\"p.main\")\n",
				"utils.lua": "return 1\n",
			},
			tweak: func(cfg *config.Config) { cfg.StripPrefix = "p" },
			want: map[string][]string{
				"main.lua":  {"p.main"},
				"utils.lua": {"p.utils"},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g, dir, err := testPrefixBuild(t, tt.files, "p", tt.tweak)
			require.NoError(t, err)
			assert.Equal(t, tt.want, testNamesByFile(t, g, dir))
		})
	}
}

// TestBuild_PackagePrefixRenamesRequires: the bundle registers the prefixed
// names only, so the requires that load a module of the project are renamed
// to the prefixed name in the code the bundle embeds, the entry's and every
// module's, in place. A name that carries the prefix already stays, and so
// does every require the bundle leaves to the runtime (the standard library,
// a runtime's built-in module, a module not found, a skipped module, a
// dynamic require) and a call of a local named require. The requires found
// are those of the file as read.
func TestBuild_PackagePrefixRenamesRequires(t *testing.T) {
	t.Parallel()

	main := "local a = require(\"a\")\n" +
		"local b = require \"p.b\"\n" +
		"local s = require(\"string\")\n" +
		"local u = require(\"utf8\")\n" +
		"local ok, m = pcall(require, \"missing\")\n" +
		"local k = require(\"skipped\")\n" +
		"local d = require(name)\n" +
		"local function load(require) return require(\"a\") end\n" +
		"return require [==[\na]==]\n"
	g, dir, err := testPrefixBuild(t, map[string]string{
		"main.lua":    main,
		"a.lua":       "return require('c'), require(\"p.c\")\n",
		"c.lua":       "return 1\n",
		"p/b.lua":     "return require(\"a\")\n",
		"skipped.lua": "return 2\n",
	}, "p", func(cfg *config.Config) {
		cfg.SkipPackages = []string{"skipped"}
		cfg.StripPrefix = "p"
	})
	require.NoError(t, err)

	sources := make(map[string]string)

	for _, mod := range g.Modules {
		rel, relErr := filepath.Rel(dir, mod.FilePath)
		require.NoError(t, relErr)

		sources[filepath.ToSlash(rel)] = string(mod.Source)
	}

	assert.Equal(t, map[string]string{
		"main.lua": "local a = require(\"p.a\")\n" +
			"local b = require \"p.b\"\n" +
			"local s = require(\"string\")\n" +
			"local u = require(\"utf8\")\n" +
			"local ok, m = pcall(require, \"missing\")\n" +
			"local k = require(\"skipped\")\n" +
			"local d = require(name)\n" +
			"local function load(require) return require(\"a\") end\n" +
			"return require [==[\np.a]==]\n",
		"a.lua":   "return require('p.c'), require(\"p.c\")\n",
		"c.lua":   "return 1\n",
		"p/b.lua": "return require(\"p.a\")\n",
	}, sources)

	names := make([]string, 0, len(g.Entry.Requires))

	for _, call := range g.Entry.Requires {
		names = append(names, call.Name)
	}

	assert.Equal(t, []string{"a", "p.b", "string", "utf8", "missing", "skipped", "", "a"}, names,
		"the requires of the entry as read")
}

// TestBuild_PackagePrefixBuiltinNames: a module required by the name of a
// module some runtime has built in is registered under the prefixed name,
// with that name to defer to where the runtime has its own module, as the
// require the prefix renames does. A name that carries the prefix already is
// served like any name of a built-in module.
func TestBuild_PackagePrefixBuiltinNames(t *testing.T) {
	t.Parallel()

	g, dir, err := testPrefixBuild(t, map[string]string{
		"main.lua":    "require(\"bit\")\nrequire(\"csv\")\nrequire(\"./csv\")\n",
		"bit.lua":     "return 1\n",
		"csv.lua":     "return 2\n",
		"jit/opt.lua": "return 3\n",
	}, "jit", func(cfg *config.Config) { cfg.IncludePackages = []string{"jit.opt"} })
	require.NoError(t, err)

	bit := g.ByPath[filepath.Join(dir, "bit.lua")]
	require.NotNil(t, bit)
	assert.Equal(t, []string{"jit.bit"}, bit.Names)
	assert.Empty(t, bit.BuiltinNames)
	assert.Equal(t, []graph.Deferral{{Name: "jit.bit", Builtin: "bit"}}, bit.Deferrals)

	csv := g.ByPath[filepath.Join(dir, "csv.lua")]
	require.NotNil(t, csv)
	assert.Equal(t, []string{"jit.csv", "jit../csv"}, csv.Names)
	assert.Empty(t, csv.BuiltinNames)
	assert.Equal(t, []graph.Deferral{{Name: "jit.csv", Builtin: "csv"}}, csv.Deferrals)

	opt := g.ByPath[filepath.Join(dir, "jit", "opt.lua")]
	require.NotNil(t, opt)
	assert.Equal(t, []string{"jit.opt"}, opt.Names)
	assert.Equal(t, []string{"jit.opt"}, opt.BuiltinNames)
	assert.Empty(t, opt.Deferrals)

	var warned []string

	for _, w := range g.Warnings {
		if w.Kind == graph.WarnShadowsBuiltin {
			warned = append(warned, w.Module)
		}
	}

	assert.Equal(t, []string{"jit.opt", "bit", "csv"}, warned, "a warning per name of a built-in module")
}

// TestBuild_PackagePrefixNameTaken: a name the package prefix generates may
// be taken already, by a module of the project that has that name or by a
// module some runtime has built in. The bundle can register one module under
// a name, so either would take the place of the other; the build fails and
// names both, so the user can choose another prefix.
func TestBuild_PackagePrefixNameTaken(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files  map[string]string
		prefix string
		// want are parts of the message, and paths the files it names,
		// relative to the project directory.
		want  []string
		paths []string
	}{
		"a module of the project": {
			files: map[string]string{
				"main.lua": "print((require(\"x\")), (require(\"p.x\")))\n",
				"x.lua":    "return \"X\"\n",
				"p/x.lua":  "return \"PX\"\n",
			},
			prefix: "p",
			want:   []string{`package prefix "p" turns "x"`, `into "p.x"`, "choose another prefix"},
			paths:  []string{"x.lua", "p/x.lua"},
		},
		"a module of the project, found through its generated name": {
			files: map[string]string{
				"main.lua": "print((require(\"z.x\")), (require(\"x\")))\n",
				"x.lua":    "return \"X\"\n",
				"z/x.lua":  "return \"ZX\"\n",
			},
			prefix: "z",
			want:   []string{`package prefix "z" turns "x"`, `into "z.x"`, "choose another prefix"},
			paths:  []string{"x.lua", "z/x.lua"},
		},
		"a module some runtime has built in": {
			files: map[string]string{
				"main.lua": "print(require(\"new\"))\n",
				"new.lua":  "return 1\n",
			},
			prefix: "table",
			want: []string{
				`package prefix "table" turns "new"`, `into "table.new"`,
				"a module LuaJIT and Tarantool have built in", "choose another prefix",
			},
			paths: []string{"new.lua"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g, dir, err := testPrefixBuild(t, tt.files, tt.prefix, nil)

			require.ErrorIs(t, err, graph.ErrNameConflict)
			assert.Nil(t, g)

			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}

			for _, path := range tt.paths {
				assert.Contains(t, err.Error(), filepath.Join(dir, filepath.FromSlash(path)))
			}
		})
	}
}
