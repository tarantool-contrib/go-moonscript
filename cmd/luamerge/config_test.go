package main

import (
	"maps"
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

// TestCLI_ListFlagsReplaceConfig checks the list flags --search, --skip and
// --include on their own and together with the matching list in
// luamerge.yaml: a list given on the command line replaces the file's list, it
// is not added to it.
func TestCLI_ListFlagsReplaceConfig(t *testing.T) {
	t.Parallel()

	// search: a.lua is found only in lib/, b.lua only in vendor/.
	searchProject := luatest.Project{
		"main.lua":     "require(\"a\")\nrequire(\"b\")\n",
		"lib/a.lua":    "return 1\n",
		"vendor/b.lua": "return 2\n",
	}
	// skip: both modules are found next to the entry.
	skipProject := luatest.Project{
		"main.lua": "require(\"a\")\nrequire(\"b\")\n",
		"a.lua":    "return 1\n",
		"b.lua":    "return 2\n",
	}
	// include: nothing requires a or b.
	includeProject := luatest.Project{
		"main.lua": "print(\"main\")\n",
		"a.lua":    "return 1\n",
		"b.lua":    "return 2\n",
	}

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

			for _, name := range tt.embedded {
				assert.Contains(t, stdout, preloaded(name), "%s must be embedded", name)
			}

			for _, name := range tt.left {
				assert.NotContains(t, stdout, preloaded(name), "%s must be left to runtime", name)
			}
		})
	}
}
