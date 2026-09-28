package graph_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/parse"
	"github.com/tarantool/go-luamerge/internal/resolve"
)

// testPrefixBuild writes files, by slash-separated path, into a fresh project
// directory and builds its graph from main.lua in strict mode with the
// package prefix prefix. It returns the graph, the directory, whose symbolic
// links are resolved as Build resolves the entry's, and Build's error.
func testPrefixBuild(t *testing.T, files map[string]string, prefix string) (*graph.Graph, string, error) {
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

	g, err := graph.Build(&cfg, parse.New(), resolve.New(cfg.Root, nil, cfg.Path))

	return g, dir, err
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

			g, dir, err := testPrefixBuild(t, tt.files, tt.prefix)

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
