package graph_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-luamerge/internal/config"
	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/parse"
	"github.com/tarantool/go-luamerge/internal/resolve"
)

const (
	requireAScript = `require("a")`
	requireBScript = `require("b")`
)

func TestGraphLinearDependency(t *testing.T) {
	t.Parallel()
	// Build resolves the symbolic links in the entry's path (on macOS the
	// temporary directory is under /var, a link to /private/var).
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	// Create Lua files: main.lua -> a.lua -> b.lua.
	mainContent := requireAScript
	aContent := requireBScript
	bContent := `print("b")`

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.lua"), []byte(mainContent), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.lua"), []byte(aContent), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b.lua"), []byte(bContent), 0600))

	cfg := config.Config{
		Entry:    filepath.Join(tmpDir, "main.lua"),
		Output:   "",
		Root:     tmpDir,
		Path:     "?.lua",
		Search:   nil,
		Strict:   true,
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    nil,
		IncludePackages: nil,
	}

	parser := parse.New()
	resolver := resolve.New(cfg.Root, nil, cfg.Path)
	g, err := graph.Build(&cfg, parser, resolver)
	require.NoError(t, err, "Build() error")

	require.NotNil(t, g.Entry, "Build() Entry is nil")
	assert.Equal(t, filepath.Join(tmpDir, "main.lua"), g.Entry.FilePath, "Entry FilePath")

	// Should have 3 modules.
	assert.Len(t, g.Modules, 3, "len(g.Modules)")

	// Check module names.
	modulesByPath := make(map[string]*graph.Module)
	for _, m := range g.Modules {
		modulesByPath[m.FilePath] = m
	}

	mainMod := modulesByPath[filepath.Join(tmpDir, "main.lua")]
	aMod := modulesByPath[filepath.Join(tmpDir, "a.lua")]
	bMod := modulesByPath[filepath.Join(tmpDir, "b.lua")]

	require.NotNil(t, mainMod, "Some modules missing")
	require.NotNil(t, aMod, "Some modules missing")
	require.NotNil(t, bMod, "Some modules missing")

	// Check requires.
	require.Len(t, mainMod.Requires, 1, "main.Requires")
	assert.Equal(t, "a", mainMod.Requires[0].Name, "main.Requires[0].Name")
	require.Len(t, aMod.Requires, 1, "a.Requires")
	assert.Equal(t, "b", aMod.Requires[0].Name, "a.Requires[0].Name")
	assert.Empty(t, bMod.Requires, "b.Requires")
}

func TestGraphCircularDependency(t *testing.T) {
	t.Parallel()
	// Build resolves the symbolic links in the entry's path, and the resolver
	// is given the root as spelled here: both must name a.lua alike.
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	// Create Lua files: a.lua -> b.lua -> a.lua (circular).
	aContent := requireBScript
	bContent := requireAScript

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.lua"), []byte(aContent), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b.lua"), []byte(bContent), 0600))

	cfg := config.Config{
		Entry:    filepath.Join(tmpDir, "a.lua"),
		Output:   "",
		Root:     tmpDir,
		Path:     "?.lua",
		Search:   nil,
		Strict:   true,
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    nil,
		IncludePackages: nil,
	}

	parser := parse.New()
	resolver := resolve.New(cfg.Root, nil, cfg.Path)
	g, err := graph.Build(&cfg, parser, resolver)
	require.NoError(t, err, "Build() error")

	// Should have 2 modules (circular is fine).
	assert.Len(t, g.Modules, 2, "len(g.Modules)")
}

func TestGraphDynamicRequireWarning(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	content := `local mod = "a"; require(mod)`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.lua"), []byte(content), 0600))

	cfg := config.Config{
		Entry:    filepath.Join(tmpDir, "main.lua"),
		Output:   "",
		Root:     tmpDir,
		Path:     "?.lua",
		Search:   nil,
		Strict:   false, // not strict, should produce warning.
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    nil,
		IncludePackages: nil,
	}

	parser := parse.New()
	resolver := resolve.New(cfg.Root, nil, cfg.Path)
	g, err := graph.Build(&cfg, parser, resolver)
	require.NoError(t, err, "Build() error")

	// Should have warning about dynamic require.
	assert.Len(t, g.Warnings, 1, "len(g.Warnings)")
}

func TestGraphStrictModeError(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	content := `require("nonexistent")`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.lua"), []byte(content), 0600))

	cfg := config.Config{
		Entry:    filepath.Join(tmpDir, "main.lua"),
		Output:   "",
		Root:     tmpDir,
		Path:     "?.lua",
		Search:   nil,
		Strict:   true, // strict mode, should error.
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    nil,
		IncludePackages: nil,
	}

	parser := parse.New()
	resolver := resolve.New(cfg.Root, nil, cfg.Path)
	_, err := graph.Build(&cfg, parser, resolver)
	require.Error(t, err, "Build() expected error for unresolved require in strict mode")
}

func TestGraphPackagePrefix(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// Create Lua files: main.lua -> a.lua.
	mainContent := requireAScript
	aContent := `return {}`

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.lua"), []byte(mainContent), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.lua"), []byte(aContent), 0600))

	cfg := config.Config{
		Entry:    filepath.Join(tmpDir, "main.lua"),
		Output:   "",
		Root:     tmpDir,
		Path:     "?.lua",
		Search:   nil,
		Strict:   true,
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "mypkg",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    nil,
		IncludePackages: nil,
	}

	parser := parse.New()
	resolver := resolve.New(cfg.Root, nil, cfg.Path)
	g, err := graph.Build(&cfg, parser, resolver)
	require.NoError(t, err, "Build() error")

	// Should have 2 modules.
	require.Len(t, g.Modules, 2, "len(g.Modules)")

	// a keeps the name main requires it by, and the prefixed name is an alias
	// of it, not a name of its own. Nothing requires the entry, so it has no
	// name to prefix.
	aMod := g.ByPath[filepath.Join(tmpDir, "a.lua")]
	require.NotNil(t, aMod)
	assert.Equal(t, []string{"a"}, aMod.Names)
	assert.Equal(t, []graph.Alias{{Name: "mypkg.a", Target: "a"}}, aMod.Aliases)
	assert.Empty(t, g.Entry.Names, "the entry's names")
	assert.Empty(t, g.Entry.Aliases, "the entry's aliases")
	assert.Equal(t, "main", g.EntryName)
}

// TestBuild_PackagePrefixAliases: --package-prefix generates an alias for
// every name that lacks the prefix and keeps it apart from the names the
// project requires the module by.
func TestBuild_PackagePrefixAliases(t *testing.T) {
	t.Parallel()

	// Build resolves the symbolic links in the entry's path; the resolver is
	// given the root as spelled here.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "p"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"),
		[]byte("require(\"x\")\nrequire(\"p.z\")\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.lua"), []byte("return 1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "p", "z.lua"), []byte("return 2\n"), 0o600))

	cfg := entryConfig(dir, "main.lua")
	cfg.PackagePrefix = "p"
	g, err := graph.Build(&cfg, parse.New(), resolve.New(cfg.Root, nil, cfg.Path))
	require.NoError(t, err)

	type names struct {
		Names   []string
		Aliases []graph.Alias
	}

	got := make(map[string]names)

	for _, mod := range g.Modules {
		rel, relErr := filepath.Rel(dir, mod.FilePath)
		require.NoError(t, relErr)

		got[filepath.ToSlash(rel)] = names{Names: mod.Names, Aliases: mod.Aliases}
	}

	assert.Equal(t, map[string]names{
		"main.lua": {Names: nil, Aliases: nil},
		"x.lua":    {Names: []string{"x"}, Aliases: []graph.Alias{{Name: "p.x", Target: "x"}}},
		"p/z.lua":  {Names: []string{"p.z"}, Aliases: nil},
	}, got)
}

// entryConfig is a strict configuration with the default path templates that
// bundles entry, relative to root.
func entryConfig(root, entry string) config.Config {
	return config.Config{
		Entry:    filepath.Join(root, entry),
		Output:   "",
		Root:     root,
		Path:     "?.lua;?/init.lua",
		Search:   nil,
		Strict:   true,
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    nil,
		IncludePackages: nil,
	}
}

// TestBuild_EntryIsNamedOnlyByItsRequires: the program runs the entry, so
// the entry has a module name only when something requires it by one. The
// name derived from its path only labels the bundle.
func TestBuild_EntryIsNamedOnlyByItsRequires(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		aSource string
		want    []string
	}{
		"not required":         {aSource: "return {}\n", want: nil},
		"required by a module": {aSource: "return require(\"app\")\n", want: []string{"app"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Build resolves the symbolic links in the entry's path; the
			// resolver is given the root as spelled here, so a require of the
			// entry must see the same spelling.
			dir, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "app.lua"), []byte(requireAScript), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a.lua"), []byte(tt.aSource), 0o600))

			cfg := entryConfig(dir, "app.lua")
			g, err := graph.Build(&cfg, parse.New(), resolve.New(cfg.Root, nil, cfg.Path))
			require.NoError(t, err)

			assert.Equal(t, tt.want, g.Entry.Names)
			assert.Equal(t, "app", g.EntryName)
		})
	}
}

func TestGraphSkipPackages(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// Create Lua files: main.lua -> a.lua, but a.lua is skipped.
	mainContent := requireAScript
	aContent := `return {}`

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.lua"), []byte(mainContent), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.lua"), []byte(aContent), 0600))

	cfg := config.Config{
		Entry:    filepath.Join(tmpDir, "main.lua"),
		Output:   "",
		Root:     tmpDir,
		Path:     "?.lua",
		Search:   nil,
		Strict:   true,
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    []string{"a"},
		IncludePackages: nil,
	}

	parser := parse.New()
	resolver := resolve.New(cfg.Root, nil, cfg.Path)
	g, err := graph.Build(&cfg, parser, resolver)
	require.NoError(t, err, "Build() error")

	// Should have only 1 module (main).
	require.Len(t, g.Modules, 1, "len(g.Modules)")
	assert.Same(t, g.Entry, g.Modules[0], "only module should be entry")
	// Should have a warning about skipped package.
	foundSkipWarning := false

	for _, w := range g.Warnings {
		if strings.Contains(w.Message, "skipped package") {
			foundSkipWarning = true
			break
		}
	}

	assert.True(t, foundSkipWarning, "expected skip warning not found")
}

func TestGraphIncludePackages(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// Create Lua files: main.lua (no requires), a.lua, b.lua.
	mainContent := `print("main")`
	aContent := requireBScript
	bContent := `print("b")`

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.lua"), []byte(mainContent), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.lua"), []byte(aContent), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b.lua"), []byte(bContent), 0600))

	cfg := config.Config{
		Entry:    filepath.Join(tmpDir, "main.lua"),
		Output:   "",
		Root:     tmpDir,
		Path:     "?.lua",
		Search:   nil,
		Strict:   true,
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    nil,
		IncludePackages: []string{"a"},
	}

	parser := parse.New()
	resolver := resolve.New(cfg.Root, nil, cfg.Path)
	g, err := graph.Build(&cfg, parser, resolver)
	require.NoError(t, err, "Build() error")

	// Should have 3 modules (main, a, b) because a includes b.
	assert.Len(t, g.Modules, 3, "len(g.Modules)")
	// Check that a and b are present.
	foundA := false
	foundB := false

	for _, mod := range g.Modules {
		base := filepath.Base(mod.FilePath)
		if base == "a.lua" {
			foundA = true
		}

		if base == "b.lua" {
			foundB = true
		}
	}

	assert.True(t, foundA, "module a.lua not found in graph")
	assert.True(t, foundB, "module b.lua not found in graph")
}

func TestGraphIncludeSkipConflict(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	mainContent := `print("main")`
	aContent := `print("a")`

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.lua"), []byte(mainContent), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.lua"), []byte(aContent), 0600))

	cfg := config.Config{
		Entry:    filepath.Join(tmpDir, "main.lua"),
		Output:   "",
		Root:     tmpDir,
		Path:     "?.lua",
		Search:   nil,
		Strict:   true,
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    []string{"a"},
		IncludePackages: []string{"a"},
	}

	parser := parse.New()
	resolver := resolve.New(cfg.Root, nil, cfg.Path)
	g, err := graph.Build(&cfg, parser, resolver)
	require.NoError(t, err, "Build() error")

	// Should have only 1 module (main) because skip takes precedence.
	assert.Len(t, g.Modules, 1, "len(g.Modules)")
	// Should have warning about skipped include.
	foundSkipWarning := false

	for _, w := range g.Warnings {
		if strings.Contains(w.Message, "include package") && strings.Contains(w.Message, "skipped") {
			foundSkipWarning = true
			break
		}
	}

	assert.True(t, foundSkipWarning, "expected skip warning for include package not found")
}

func TestWarningKind_String(t *testing.T) {
	t.Parallel()

	for kind, want := range map[graph.WarningKind]string{
		graph.WarnDynamicRequire:  "dynamic-require",
		graph.WarnSkipped:         "skipped",
		graph.WarnUnresolved:      "unresolved",
		graph.WarnCModule:         "c-module",
		graph.WarnNonLua:          "non-lua",
		graph.WarnIndirectRequire: "indirect-require",
		graph.WarningKind(-1):     "unknown",
	} {
		assert.Equal(t, want, kind.String(), "WarningKind(%d)", int(kind))
	}
}

// testBuild writes files into a fresh project directory and builds its graph
// from main.lua in strict mode. It returns the graph and the directory, whose
// symbolic links are resolved as Build resolves the entry's (on macOS the
// temporary directory is under /var, a link to /private/var).
func testBuild(t *testing.T, files map[string]string) (*graph.Graph, string) {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}

	cfg := config.Default()
	cfg.Entry = filepath.Join(dir, "main.lua")
	cfg.Root = dir
	cfg.Path = "?.lua"
	cfg.Strict = true

	g, err := graph.Build(&cfg, parse.New(), resolve.New(cfg.Root, nil, cfg.Path))
	require.NoError(t, err, "Build() error")

	return g, dir
}

// testModuleNames is every name under which g embeds a module. The entry
// runs as the bundle's main chunk and has no name unless something requires
// it.
func testModuleNames(g *graph.Graph) []string {
	var names []string
	for _, mod := range g.Modules {
		names = append(names, mod.Names...)
	}

	return names
}

// TestBuild_IndirectRequire: of the indirect forms of require, those that name
// their module literally are embedded, and each of the others leaves a warning
// of the right kind at the right line instead of nothing. Strict mode does not
// turn these warnings into errors.
func TestBuild_IndirectRequire(t *testing.T) {
	t.Parallel()

	g, dir := testBuild(t, map[string]string{
		"main.lua": `local r = require; print((r("m1")))
print((_G.require("m2")))
print(select(2, xpcall(require, debug.traceback, "m3")))
local name = "m4"; print(select(2, pcall(require, name)))
print(load_all(require, { "m5" })[1])
`,
		"m1.lua": `return "m1"`,
		"m2.lua": `return "m2"`,
		"m3.lua": `return "m3"`,
		"m4.lua": `return "m4"`,
		"m5.lua": `return "m5"`,
	})

	assert.ElementsMatch(t, []string{"m2", "m3"}, testModuleNames(g), "embedded modules")

	mainPath := filepath.Join(dir, "main.lua")
	assert.Equal(t, []graph.Warning{
		{
			Kind:   graph.WarnIndirectRequire,
			Module: "",
			File:   mainPath,
			Line:   1,
			Message: `require aliased as local "r" at line 1, ` +
				"cannot resolve the modules loaded through it",
		},
		{
			Kind:    graph.WarnDynamicRequire,
			Module:  "",
			File:    mainPath,
			Line:    4,
			Message: "dynamic require at line 4, cannot resolve",
		},
		{
			Kind:   graph.WarnIndirectRequire,
			Module: "",
			File:   mainPath,
			Line:   5,
			Message: "require used as a value at line 5, " +
				"cannot resolve the modules loaded through it",
		},
	}, g.Warnings)
}

// TestBuild_CachedGlobalRequire: `local require = require` caches the global,
// and calls through the local are found by name, so it is neither a warning
// nor a reason to miss the module.
func TestBuild_CachedGlobalRequire(t *testing.T) {
	t.Parallel()

	g, _ := testBuild(t, map[string]string{
		"main.lua": "local require = require\nlocal x = require(\"x\")\n",
		"x.lua":    "return {}\n",
	})

	assert.ElementsMatch(t, []string{"x"}, testModuleNames(g), "embedded modules")
	assert.Empty(t, g.Warnings)
}
