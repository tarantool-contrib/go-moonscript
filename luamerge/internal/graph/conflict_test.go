package graph_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/config"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/graph"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/resolve"
)

// queueResolver answers each name with the files queued for it, one per
// call, the last one repeating: a resolver that can give one name a
// different file each time it is asked, as one that looked next to the
// requiring file did.
type queueResolver struct {
	files map[string][]string
}

func (q *queueResolver) Resolve(name string) (*resolve.Result, error) {
	queue := q.files[name]
	if len(queue) == 0 {
		return nil, fmt.Errorf("queueResolver: %q: %w", name, resolve.ErrModuleNotFound)
	}

	if len(queue) > 1 {
		q.files[name] = queue[1:]
	}

	return &resolve.Result{FilePath: queue[0], ModName: name}, nil
}

// TestBuild_NameConflict checks that Build refuses a module name registered
// for two different files: the bundle carries one loader per name, so one of
// the files would silently replace the other for every caller.
func TestBuild_NameConflict(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for name, content := range map[string]string{
		"main.lua":     "require(\"util\")\nrequire(\"sub.x\")\n",
		"util.lua":     "return 1\n",
		"sub/util.lua": "return 2\n",
		"sub/x.lua":    "require(\"util\")\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	cfg := config.Default()
	cfg.Entry = filepath.Join(dir, "main.lua")
	cfg.Root = dir
	resolver := &queueResolver{files: map[string][]string{
		"util":  {filepath.Join(dir, "util.lua"), filepath.Join(dir, "sub", "util.lua")},
		"sub.x": {filepath.Join(dir, "sub", "x.lua")},
	}}

	g, err := graph.Build(&cfg, parse.New(), resolver)

	require.ErrorIs(t, err, graph.ErrNameConflict)
	assert.Nil(t, g)
	assert.Contains(t, err.Error(), `"util"`)
	assert.Contains(t, err.Error(), filepath.Join(dir, "util.lua"))
	assert.Contains(t, err.Error(), filepath.Join(dir, "sub", "util.lua"))
}
