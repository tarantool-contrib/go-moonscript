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
	tmpDir := t.TempDir()

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
			StripShebang:     false,
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
	tmpDir := t.TempDir()

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
			StripShebang:     false,
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
			StripShebang:     false,
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
			StripShebang:     false,
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
			StripShebang:     false,
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
	assert.Len(t, g.Modules, 2, "len(g.Modules)")

	// Check module names.
	for _, mod := range g.Modules {
		// Each module should have at least 2 names (original and prefixed).
		assert.GreaterOrEqual(t, len(mod.Names), 2, "module %v has too few names", mod.FilePath)
		// First name for entry should be prefixed.
		if mod == g.Entry {
			assert.True(
				t, strings.HasPrefix(mod.Names[0], "mypkg."),
				"entry module first name = %q, want prefixed with 'mypkg.'", mod.Names[0],
			)
		}
		// Check that both original and prefixed names exist.
		hasOriginal := false
		hasPrefixed := false

		for _, name := range mod.Names {
			if name == "main" || name == "a" {
				hasOriginal = true
			}

			if strings.HasPrefix(name, "mypkg.") {
				hasPrefixed = true
			}
		}

		assert.True(t, hasOriginal, "module missing original name")
		assert.True(t, hasPrefixed, "module missing prefixed name")
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
			StripShebang:     false,
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
			StripShebang:     false,
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
			StripShebang:     false,
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
