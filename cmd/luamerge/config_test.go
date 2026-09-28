package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// TestCLI_RootSpelling checks that the entry's name does not depend on how
// the root and the entry are spelled. The name is the entry's path relative
// to the root: a relative --root named the entry main instead of app.main,
// and a root reached through a symbolic link gave .......src.app.main.
func TestCLI_RootSpelling(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{
		"src/app/main.lua": "print(require(\"app.util\"))\n",
		"src/app/util.lua": "return \"util\"\n",
	})
	require.NoError(t, os.Symlink(".", filepath.Join(dir, "link")))

	tests := []struct {
		name string
		yaml string // luamerge.yaml; "" writes none.
		args []string
	}{
		{"relative root", "", []string{"--entry", "src/app/main.lua", "--root", "src"}},
		{"absolute root", "", []string{"--entry", "src/app/main.lua", "--root", filepath.Join(dir, "src")}},
		{"root through a link", "",
			[]string{"--entry", "src/app/main.lua", "--root", filepath.Join(dir, "link", "src")}},
		{"entry through a link", "", []string{"--entry", "link/src/app/main.lua", "--root", "src"}},
		{"relative root in luamerge.yaml", "root: src\n", []string{"--entry", "src/app/main.lua"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange: the config file goes into a directory of its own, so
			// that parallel cases do not see each other's.
			configPath := filepath.Join(t.TempDir(), "luamerge.yaml")
			require.NoError(t, os.WriteFile(configPath, []byte(tt.yaml), 0o600))

			// Act.
			args := slices.Concat([]string{"--config", configPath, "--package-prefix", "pk"}, tt.args)
			stdout, stderr, code := runCLIIn(t, dir, args...)

			// Assert: the entry is not registered, so its name shows in the
			// bundle's header only.
			require.Equal(t, 0, code, stderr)
			assert.Empty(t, stderr)
			assert.Contains(t, stdout, entryHeader("app.main"))
			assertEmbedded(t, stdout, []string{"app.util", "pk.app.util"}, nil)
		})
	}
}

// entryHeader is the bundle's header line that names the entry.
func entryHeader(name string) string {
	return "-- Entry: " + name + "\n"
}

// TestCLI_EntryOutsideRoot checks that an entry kept outside the root is
// named after its file, whatever the root's spelling: its path relative to
// the root would start with "..".
func TestCLI_EntryOutsideRoot(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{
		"scripts/run.lua": "print(require(\"util\"))\n",
		"src/util.lua":    "return \"util\"\n",
	})

	for _, root := range []string{"src", filepath.Join(dir, "src")} {
		t.Run(root, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := runCLIIn(t, dir, "--entry", "scripts/run.lua", "--root", root)

			require.Equal(t, 0, code, stderr)
			assert.Contains(t, stdout, entryHeader("run"))
			assertEmbedded(t, stdout, []string{"util"}, nil)
			// The name from the path relative to the root: ...scripts.run.
			assert.NotContains(t, stdout, "scripts.run", "the entry is named by a path out of the root")
		})
	}
}

// TestCLI_MissingRoot checks that a --root that does not exist is an error,
// not a build that finds no module.
func TestCLI_MissingRoot(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{"main.lua": "print(\"hi\")\n"})

	stdout, stderr, code := runCLIIn(t, dir, "--entry", "main.lua", "--root", "scr")

	assert.Equal(t, 1, code, "exit code")
	assert.Contains(t, stderr, "scr")
	assert.Contains(t, stderr, "no such file or directory")
	assert.Empty(t, stdout)
}

// TestCLI_PackageNameOverride checks that --package-name overrides the
// package_name of luamerge.yaml or the environment. The configured name used
// to be applied to strip_prefix and package_prefix while loading, and the
// flag's then filled nothing: the bundle kept the configured name.
func TestCLI_PackageNameOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string   // luamerge.yaml; "" writes none.
		env  []string // KEY=VALUE pairs.
		args []string // Added to --entry main.lua.
	}{
		{"flag over config", "package_name: other\n", nil, []string{"--package-name", "lib"}},
		{"flag over environment", "", []string{"LUAMERGE_PACKAGE_NAME=other"}, []string{"--package-name", "lib"}},
		{"environment over config", "package_name: other\n", []string{"LUAMERGE_PACKAGE_NAME=lib"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange: modules named lib.* in a flat layout.
			project := luatest.Project{
				"main.lua": "print(require(\"lib.util\"))\n",
				"util.lua": "return 1\n",
			}
			if tt.yaml != "" {
				project["luamerge.yaml"] = tt.yaml
			}

			dir := luatest.WriteProject(t, project)

			// Act.
			args := append([]string{"--entry", "main.lua", "--strict"}, tt.args...)
			result := luatest.RunCLI(t, dir, tt.env, args...)

			// Assert: lib.util resolves under --strict only by stripping lib.
			require.Equal(t, 0, result.ExitCode, result.Stderr)
			assertEmbedded(t, result.Stdout, []string{"lib.util"}, nil)
			assert.NotContains(t, result.Stdout, "other.")
		})
	}
}

// TestCLI_ConfigPathsFromWorkingDirectory checks that relative paths in a
// config file are taken from the working directory, not from the file's
// directory: entry, root and search of build/luamerge.yaml name ./src and
// ./lib. Decoys under build/ would be bundled were the paths taken from
// there.
func TestCLI_ConfigPathsFromWorkingDirectory(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{
		"src/main.lua": "print(require(\"a\"), require(\"b\"))\n",
		"src/a.lua":    "return \"a from src\"\n",
		"lib/b.lua":    "return \"b from lib\"\n",
		// Decoys.
		"build/src/main.lua": "print(\"DECOY_ENTRY\")\n",
		"build/src/a.lua":    "return \"DECOY_A\"\n",
		"build/lib/b.lua":    "return \"DECOY_B\"\n",
		"build/luamerge.yaml": "entry: src/main.lua\nroot: src\nsearch: [lib]\nstrict: true\n" +
			"output: out/bundle.lua\n",
	})

	stdout, stderr, code := runCLIIn(t, dir, "--config", "build/luamerge.yaml")

	require.Equal(t, 0, code, stderr)
	assert.Empty(t, stdout)
	assert.NoFileExists(t, filepath.Join(dir, "build", "out", "bundle.lua"))

	bundle := readFile(t, filepath.Join(dir, "out", "bundle.lua"))
	assert.Contains(t, bundle, "a from src")
	assert.Contains(t, bundle, "b from lib")
	assert.NotContains(t, bundle, "DECOY")
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
