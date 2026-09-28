package main

import (
	"maps"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/luatest"
)

// Tests for how the CLI combines its flags with luamerge.yaml and the
// environment.

// preloaded is the registration of module name in a bundle.
func preloaded(name string) string {
	return `package.preload["` + name + `"]`
}

// Projects for the list settings, each with the modules a and b.
var (
	// searchProject has a.lua only in lib/ and b.lua only in vendor/.
	searchProject = luatest.Project{ //nolint:gochecknoglobals // read-only test fixture
		"main.lua":     "require(\"a\")\nrequire(\"b\")\n",
		"lib/a.lua":    "return 1\n",
		"vendor/b.lua": "return 2\n",
	}
	// skipProject has both modules next to the entry, which requires them.
	skipProject = luatest.Project{ //nolint:gochecknoglobals // read-only test fixture
		"main.lua": "require(\"a\")\nrequire(\"b\")\n",
		"a.lua":    "return 1\n",
		"b.lua":    "return 2\n",
	}
	// includeProject has both modules next to the entry, which requires
	// neither.
	includeProject = luatest.Project{ //nolint:gochecknoglobals // read-only test fixture
		"main.lua": "print(\"main\")\n",
		"a.lua":    "return 1\n",
		"b.lua":    "return 2\n",
	}
)

// assertEmbedded checks that bundle embeds the modules embedded and none of
// the modules left.
func assertEmbedded(t *testing.T, bundle string, embedded, left []string) {
	t.Helper()

	for _, name := range embedded {
		assert.Contains(t, bundle, preloaded(name), "%s must be embedded", name)
	}

	for _, name := range left {
		assert.NotContains(t, bundle, preloaded(name), "%s must be left to runtime", name)
	}
}

// TestCLI_ListFlagsReplaceConfig checks the list flags --search, --skip and
// --include on their own and together with the matching list in
// luamerge.yaml: a list given on the command line replaces the file's list, it
// is not added to it.
func TestCLI_ListFlagsReplaceConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		project  luatest.Project
		yaml     string   // luamerge.yaml; "" writes none.
		args     []string // Added to --entry main.lua.
		embedded []string
		left     []string // Modules the bundle must not embed.
	}{
		{"search flag", searchProject, "", []string{"--search", "lib"}, []string{"a"}, []string{"b"}},
		{"search flag repeated", searchProject, "", []string{"--search", "lib", "--search", "vendor"},
			[]string{"a", "b"}, nil},
		{"search in config", searchProject, "search: [lib]\n", nil, []string{"a"}, []string{"b"}},
		{"search flag replaces config", searchProject, "search: [lib]\n", []string{"--search", "vendor"},
			[]string{"b"}, []string{"a"}},
		{"skip flag", skipProject, "", []string{"--skip", "a"}, []string{"b"}, []string{"a"}},
		{"skip in config", skipProject, "skip_packages: [a]\n", nil, []string{"b"}, []string{"a"}},
		{"skip flag replaces config", skipProject, "skip_packages: [a]\n", []string{"--skip", "b"},
			[]string{"a"}, []string{"b"}},
		{"include flag", includeProject, "", []string{"--include", "a"}, []string{"a"}, []string{"b"}},
		{"include in config", includeProject, "include_packages: [a]\n", nil, []string{"a"}, []string{"b"}},
		{"include flag replaces config", includeProject, "include_packages: [a]\n", []string{"--include", "b"},
			[]string{"b"}, []string{"a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			project := maps.Clone(tt.project)
			if tt.yaml != "" {
				project["luamerge.yaml"] = tt.yaml
			}

			dir := luatest.WriteProject(t, project)

			// Act.
			stdout, stderr, code := runCLIIn(t, dir, append([]string{"--entry", "main.lua"}, tt.args...)...)

			// Assert.
			require.Equal(t, 0, code, stderr)
			assertEmbedded(t, stdout, tt.embedded, tt.left)
		})
	}
}

// TestCLI_ListValues checks that a list is given the same way on the command
// line and in the environment: as comma-separated items, which a flag may
// also spread over repetitions. An empty value is the empty list, which
// replaces the list of luamerge.yaml like any other. The environment used to
// refuse every list, and a flag took "a,b" for one item.
func TestCLI_ListValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		project  luatest.Project
		yaml     string   // luamerge.yaml; "" writes none.
		env      []string // KEY=VALUE pairs.
		args     []string // Added to --entry main.lua.
		embedded []string
		left     []string // Modules the bundle must not embed.
	}{
		{"search flag", searchProject, "", nil, []string{"--search", "lib,vendor"}, []string{"a", "b"}, nil},
		{"skip flag", skipProject, "", nil, []string{"--skip", "a, b"}, nil, []string{"a", "b"}},
		{"include flag", includeProject, "", nil, []string{"--include", "a,b"}, []string{"a", "b"}, nil},
		{"flag items and repetitions", includeProject, "", nil, []string{"--include", "a,", "--include", "b"},
			[]string{"a", "b"}, nil},
		{"empty skip flag", skipProject, "skip_packages: [a]\n", nil, []string{"--skip", ""},
			[]string{"a", "b"}, nil},
		{"search variable", searchProject, "", []string{"LUAMERGE_SEARCH=lib,vendor"}, nil,
			[]string{"a", "b"}, nil},
		{"skip variable", skipProject, "", []string{"LUAMERGE_SKIP_PACKAGES=a,b"}, nil, nil, []string{"a", "b"}},
		{"include variable", includeProject, "", []string{"LUAMERGE_INCLUDE_PACKAGES=a,b"}, nil,
			[]string{"a", "b"}, nil},
		{"variable replaces config", skipProject, "skip_packages: [a]\n", []string{"LUAMERGE_SKIP_PACKAGES=b"},
			nil, []string{"a"}, []string{"b"}},
		{"empty skip variable", skipProject, "skip_packages: [a]\n", []string{"LUAMERGE_SKIP_PACKAGES="}, nil,
			[]string{"a", "b"}, nil},
		{"flag replaces variable", skipProject, "", []string{"LUAMERGE_SKIP_PACKAGES=a"}, []string{"--skip", "b"},
			[]string{"a"}, []string{"b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			project := maps.Clone(tt.project)
			if tt.yaml != "" {
				project["luamerge.yaml"] = tt.yaml
			}

			dir := luatest.WriteProject(t, project)

			// Act.
			result := luatest.RunCLI(t, dir, tt.env, append([]string{"--entry", "main.lua"}, tt.args...)...)

			// Assert.
			require.Equal(t, 0, result.ExitCode, result.Stderr)
			assertEmbedded(t, result.Stdout, tt.embedded, tt.left)
		})
	}
}

// commentProject is a project whose entry starts with a comment, so whether
// a configuration that turns on remove_comments was read shows in the bundle.
// With a config file: build/luamerge.yaml does that.
func commentProject(t *testing.T) string {
	t.Helper()

	return luatest.WriteProject(t, luatest.Project{
		"main.lua":            "-- COMMENT_MARK\nprint(\"hi\")\n",
		"build/luamerge.yaml": "transform:\n  remove_comments: true\n",
	})
}

// TestCLI_ConfigNamedFile checks that --config reads the file it names, and
// that a named file that does not exist is an error, not a build without its
// settings.
func TestCLI_ConfigNamedFile(t *testing.T) {
	t.Parallel()

	dir := commentProject(t)

	t.Run("exists", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, code := runCLIIn(t, dir, "--config", "build/luamerge.yaml", "--entry", "main.lua")

		require.Equal(t, 0, code, stderr)
		assert.NotContains(t, stdout, "COMMENT_MARK", "the config file was not read")
	})

	for _, name := range []string{"build/luamerge.yml", "luamerge.yaml"} {
		t.Run("missing "+name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := runCLIIn(t, dir, "--config", name, "--entry", "main.lua")

			assert.Equal(t, 1, code, "exit code")
			assert.Contains(t, stderr, name)
			assert.Contains(t, stderr, "no such file or directory")
			assert.Empty(t, stdout)
		})
	}
}

// TestCLI_ConfigWithoutSettings checks that a config file holding no
// settings, the default one or one named with --config, gives the default
// build instead of a parse error.
func TestCLI_ConfigWithoutSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files luatest.Project // Added to the project.
		args  []string        // Added to --entry main.lua.
	}{
		{"empty luamerge.yaml", luatest.Project{"luamerge.yaml": ""}, nil},
		{"empty named file", luatest.Project{"empty.yaml": ""}, []string{"--config", "empty.yaml"}},
		{"comments only", luatest.Project{"luamerge.yaml": "# nothing yet\n"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			project := maps.Clone(tt.files)
			project["main.lua"] = "-- COMMENT_MARK\nrequire(\"m\")\n"
			project["m.lua"] = "return 1\n"
			dir := luatest.WriteProject(t, project)

			// Act.
			stdout, stderr, code := runCLIIn(t, dir, append([]string{"--entry", "main.lua"}, tt.args...)...)

			// Assert: the defaults, i.e. stdout, the arg line in the module's
			// wrapper, no transforms.
			require.Equal(t, 0, code, stderr)
			assert.Empty(t, stderr)
			assert.Contains(t, stdout, preloaded("m"))
			assert.Contains(t, stdout, `arg = rawget(_G, "arg")`)
			assert.Contains(t, stdout, "COMMENT_MARK")
		})
	}
}

// TestCLI_ConfigEmptyMeansDefault checks that an empty --config value is the
// default file, luamerge.yaml, wherever the file is used: it is read, and it
// is protected as an input. The protection used to look at a file named "",
// so an empty --config with --output luamerge.yaml replaced the config file
// with the bundle.
func TestCLI_ConfigEmptyMeansDefault(t *testing.T) {
	t.Parallel()

	const settings = "transform:\n  remove_comments: true\n"

	t.Run("read", func(t *testing.T) {
		t.Parallel()

		dir := luatest.WriteProject(t, luatest.Project{
			"main.lua":      "-- COMMENT_MARK\nprint(\"hi\")\n",
			"luamerge.yaml": settings,
		})

		stdout, stderr, code := runCLIIn(t, dir, "--config", "", "--entry", "main.lua")

		require.Equal(t, 0, code, stderr)
		assert.NotContains(t, stdout, "COMMENT_MARK", "luamerge.yaml was not read")
	})

	t.Run("absent", func(t *testing.T) {
		t.Parallel()

		dir := luatest.WriteProject(t, luatest.Project{"main.lua": "print(\"hi\")\n"})

		stdout, stderr, code := runCLIIn(t, dir, "--config", "", "--entry", "main.lua")

		require.Equal(t, 0, code, stderr)
		assert.Contains(t, stdout, "print(\"hi\")", "the entry was not bundled")
	})

	t.Run("protected", func(t *testing.T) {
		t.Parallel()

		dir := luatest.WriteProject(t, luatest.Project{
			"main.lua":      "print(\"hi\")\n",
			"luamerge.yaml": settings,
		})

		stdout, stderr, code := runCLIIn(t, dir, "--config", "", "--entry", "main.lua", "--output", "luamerge.yaml")

		assert.Equal(t, 1, code, "exit code")
		assert.Contains(t, stderr, "refusing to overwrite")
		assert.Empty(t, stdout)
		assert.Equal(t, settings, readFile(t, filepath.Join(dir, "luamerge.yaml")), "luamerge.yaml changed")
	})
}
