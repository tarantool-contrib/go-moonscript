package emit_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-luamerge/internal/emit"
	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/parse"
)

func TestEmitSingleModule(t *testing.T) {
	t.Parallel()

	g := &graph.Graph{
		Modules: []*graph.Module{
			{
				ID:       0,
				FilePath: "/test/main.lua",
				Names:    []string{"main"},
				Source:   []byte("print('hello')"),
				Requires: []parse.RequireInfo{},
			},
		},
		ByPath: map[string]*graph.Module{
			"/test/main.lua": {
				ID:       0,
				FilePath: "/test/main.lua",
				Names:    []string{"main"},
				Source:   []byte("print('hello')"),
				Requires: []parse.RequireInfo{},
			},
		},
		Entry: &graph.Module{
			ID:       0,
			FilePath: "/test/main.lua",
			Names:    []string{"main"},
			Source:   []byte("print('hello')"),
			Requires: []parse.RequireInfo{},
		},
		Warnings: []graph.Warning{},
	}

	var buf bytes.Buffer
	require.NoError(t, emit.Emit(&buf, g, nil, emit.Options{
		Prefix:   "",
		Suffix:   "",
		Debug:    false,
		Fallback: false,
		Shebang:  "",
		NoArgFix: false,
	}))

	output := buf.String()

	expectedLines := []string{
		"-- Amalgamated by luamerge",
		"-- Entry: main",
		"package.preload[\"main\"] = function(...)",
		"\nprint('hello')\n",
		"end",
		"require(\"main\")",
	}
	for _, line := range expectedLines {
		assert.Contains(t, output, line, "Output missing line %q", line)
	}
}

func TestEmitMultiModule(t *testing.T) {
	t.Parallel()

	g := &graph.Graph{
		Modules: []*graph.Module{
			{
				ID:       0,
				FilePath: "/test/main.lua",
				Names:    []string{"main"},
				Source:   []byte("require('a')"),
				Requires: []parse.RequireInfo{{Name: "a", Line: 1, Static: true, Indirect: ""}},
			},
			{
				ID:       1,
				FilePath: "/test/a.lua",
				Names:    []string{"a"},
				Source:   []byte("print('a')"),
				Requires: []parse.RequireInfo{},
			},
		},
		ByPath: map[string]*graph.Module{
			"/test/main.lua": {
				ID:       0,
				FilePath: "/test/main.lua",
				Names:    []string{"main"},
				Source:   []byte("require('a')"),
				Requires: []parse.RequireInfo{{Name: "a", Line: 1, Static: true, Indirect: ""}},
			},
			"/test/a.lua": {
				ID:       1,
				FilePath: "/test/a.lua",
				Names:    []string{"a"},
				Source:   []byte("print('a')"),
				Requires: []parse.RequireInfo{},
			},
		},
		Entry: &graph.Module{
			ID:       0,
			FilePath: "/test/main.lua",
			Names:    []string{"main"},
			Source:   []byte("require('a')"),
			Requires: []parse.RequireInfo{{Name: "a", Line: 1, Static: true, Indirect: ""}},
		},
		Warnings: []graph.Warning{},
	}

	var buf bytes.Buffer
	require.NoError(t, emit.Emit(&buf, g, nil, emit.Options{
		Prefix:   "",
		Suffix:   "",
		Debug:    false,
		Fallback: false,
		Shebang:  "",
		NoArgFix: false,
	}))

	output := buf.String()
	// Should contain both modules.
	assert.Contains(t, output, "package.preload[\"main\"]", "Output missing main module")
	assert.Contains(t, output, "package.preload[\"a\"]", "Output missing a module")
	assert.Contains(t, output, "require(\"main\")", "Output missing entry require")
}

func TestEmitModuleWithAliases(t *testing.T) {
	t.Parallel()

	g := &graph.Graph{
		Modules: []*graph.Module{
			{
				ID:       0,
				FilePath: "/test/foo.lua",
				Names:    []string{"foo", "foo.bar", "foo/bar"},
				Source:   []byte("return {}"),
				Requires: []parse.RequireInfo{},
			},
		},
		ByPath: map[string]*graph.Module{
			"/test/foo.lua": {
				ID:       0,
				FilePath: "/test/foo.lua",
				Names:    []string{"foo", "foo.bar", "foo/bar"},
				Source:   []byte("return {}"),
				Requires: []parse.RequireInfo{},
			},
		},
		Entry: &graph.Module{
			ID:       0,
			FilePath: "/test/foo.lua",
			Names:    []string{"foo"},
			Source:   []byte("return {}"),
			Requires: []parse.RequireInfo{},
		},
		Warnings: []graph.Warning{},
	}

	var buf bytes.Buffer
	require.NoError(t, emit.Emit(&buf, g, nil, emit.Options{
		Prefix:   "",
		Suffix:   "",
		Debug:    false,
		Fallback: false,
		Shebang:  "",
		NoArgFix: false,
	}))

	output := buf.String()
	// Primary name carries the real loader; aliases delegate to it so the
	// module body runs exactly once regardless of which name is required.
	assert.Contains(t, output, "package.preload[\"foo\"] = function(...)", "Output missing primary loader for foo")
	assert.Contains(t, output, "package.preload[\"foo.bar\"] = function(...) return require(\"foo\") end",
		"Output missing delegating alias foo.bar")
	assert.Contains(t, output, "package.preload[\"foo/bar\"] = function(...) return require(\"foo\") end",
		"Output missing delegating alias foo/bar")
}

func TestEmitSuffix(t *testing.T) {
	t.Parallel()

	g := &graph.Graph{
		Modules: []*graph.Module{
			{
				ID:       0,
				FilePath: "/test/main.lua",
				Names:    []string{"main"},
				Source:   []byte("print('hello')"),
				Requires: []parse.RequireInfo{},
			},
		},
		ByPath: map[string]*graph.Module{
			"/test/main.lua": {
				ID:       0,
				FilePath: "/test/main.lua",
				Names:    []string{"main"},
				Source:   []byte("print('hello')"),
				Requires: []parse.RequireInfo{},
			},
		},
		Entry: &graph.Module{
			ID:       0,
			FilePath: "/test/main.lua",
			Names:    []string{"main"},
			Source:   []byte("print('hello')"),
			Requires: []parse.RequireInfo{},
		},
		Warnings: []graph.Warning{},
	}

	var buf bytes.Buffer

	suffixCode := "print('suffix')"
	require.NoError(t, emit.Emit(&buf, g, nil, emit.Options{
		Prefix:   "",
		Suffix:   suffixCode,
		Debug:    false,
		Fallback: false,
		Shebang:  "",
		NoArgFix: false,
	}))

	output := buf.String()
	assert.Contains(t, output, "print('suffix')", "Output missing suffix code")
	// Ensure suffix appears after require.
	requireIdx := strings.Index(output, "require(\"main\")")
	suffixIdx := strings.Index(output, "print('suffix')")
	assert.False(t, requireIdx == -1 || suffixIdx == -1 || suffixIdx < requireIdx,
		"Suffix code should appear after require")
}

func TestEmitFallback(t *testing.T) {
	t.Parallel()

	mod := &graph.Module{
		ID:       0,
		FilePath: "/test/main.lua",
		Names:    []string{"main"},
		Source:   []byte("print('hello')"),
		Requires: []parse.RequireInfo{},
	}
	g := &graph.Graph{
		Modules:  []*graph.Module{mod},
		ByPath:   map[string]*graph.Module{"/test/main.lua": mod},
		Entry:    mod,
		Warnings: []graph.Warning{},
	}

	var buf bytes.Buffer
	require.NoError(t, emit.Emit(&buf, g, nil, emit.Options{
		Prefix:   "",
		Suffix:   "",
		Debug:    false,
		Fallback: true,
		Shebang:  "",
		NoArgFix: false,
	}))

	output := buf.String()
	// Fallback registers in package.postload behind an appended searcher, not
	// in package.preload.
	assert.Contains(t, output, "package.postload[\"main\"] = function(...)",
		"fallback should register in package.postload")
	assert.Contains(t, output, "searchers[#searchers+1] = function(mod)", "fallback should append a searcher")
	assert.NotContains(t, output, "package.preload[", "fallback should not use package.preload")
}

func TestEmitShebang(t *testing.T) {
	t.Parallel()

	mod := &graph.Module{
		ID:       0,
		FilePath: "/test/main.lua",
		Names:    []string{"main"},
		Source:   []byte("print('hello')"),
		Requires: []parse.RequireInfo{},
	}
	g := &graph.Graph{
		Modules:  []*graph.Module{mod},
		ByPath:   map[string]*graph.Module{"/test/main.lua": mod},
		Entry:    mod,
		Warnings: []graph.Warning{},
	}

	var buf bytes.Buffer
	require.NoError(t, emit.Emit(&buf, g, nil, emit.Options{
		Prefix:   "",
		Suffix:   "",
		Debug:    false,
		Fallback: false,
		Shebang:  "#!/usr/bin/env lua",
		NoArgFix: false,
	}))

	output := buf.String()
	// The shebang must be the very first line of the file.
	assert.True(t, strings.HasPrefix(output, "#!/usr/bin/env lua\n"),
		"shebang must be the first line, got:\n%s", output)
}

func TestEmitNoArgFix(t *testing.T) {
	t.Parallel()

	mod := &graph.Module{
		ID:       0,
		FilePath: "/test/main.lua",
		Names:    []string{"main"},
		Source:   []byte("print('hello')"),
		Requires: []parse.RequireInfo{},
	}
	g := &graph.Graph{
		Modules:  []*graph.Module{mod},
		ByPath:   map[string]*graph.Module{"/test/main.lua": mod},
		Entry:    mod,
		Warnings: []graph.Warning{},
	}

	var onBuf, offBuf bytes.Buffer
	require.NoError(t, emit.Emit(&onBuf, g, nil, emit.Options{
		Prefix:   "",
		Suffix:   "",
		Debug:    false,
		Fallback: false,
		Shebang:  "",
		NoArgFix: false,
	}))
	require.NoError(t, emit.Emit(&offBuf, g, nil, emit.Options{
		Prefix:   "",
		Suffix:   "",
		Debug:    false,
		Fallback: false,
		Shebang:  "",
		NoArgFix: true,
	}))

	assert.Contains(t, onBuf.String(), "local arg = _G.arg", "arg alias should be present by default")
	assert.NotContains(t, offBuf.String(), "local arg = _G.arg", "NoArgFix should omit the arg alias")
}

func TestEmitPreservesMultilineString(t *testing.T) {
	t.Parallel()

	// A module whose body contains a multi-line [[ ... ]] string literal with
	// internal indentation. Emitting must not reindent the source, or the
	// string's contents would change.
	src := "return [[\nalpha\n  beta\ngamma\n]]\n"
	mod := &graph.Module{
		ID:       0,
		FilePath: "/test/main.lua",
		Names:    []string{"main"},
		Source:   []byte(src),
		Requires: []parse.RequireInfo{},
	}
	g := &graph.Graph{
		Modules:  []*graph.Module{mod},
		ByPath:   map[string]*graph.Module{"/test/main.lua": mod},
		Entry:    mod,
		Warnings: []graph.Warning{},
	}

	var buf bytes.Buffer
	require.NoError(t, emit.Emit(&buf, g, nil, emit.Options{
		Prefix:   "",
		Suffix:   "",
		Debug:    false,
		Fallback: false,
		Shebang:  "",
		NoArgFix: false,
	}))

	// The literal's lines must appear verbatim, not shifted by added indentation.
	assert.Contains(t, buf.String(), "\nalpha\n  beta\ngamma\n",
		"multi-line string contents must be preserved verbatim")
}
