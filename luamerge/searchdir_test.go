package luamerge_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge"
)

// TestBundle_SearchDirectoryThroughALink: a search directory spelled through
// a symbolic link gives its modules the paths its resolved spelling gives
// them, in Result.Files and in the chunk names of a --debug bundle. The link
// stayed in the paths, as it does not in the root's or the entry's.
func TestBundle_SearchDirectoryThroughALink(t *testing.T) {
	t.Parallel()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "vendor"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "main.lua"), []byte("require(\"v\")\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vendor", "v.lua"), []byte("return 1\n"), 0o600))
	require.NoError(t, os.Symlink("vendor", filepath.Join(dir, "vlink")))

	bundle := func(search string) (*luamerge.Result, string) {
		opts := luamerge.DefaultOptions()
		opts.Entry = filepath.Join(dir, "src", "main.lua")
		opts.Search = []string{search}
		opts.Debug = true

		var buf bytes.Buffer

		res, err := luamerge.Bundle(opts, &buf)
		require.NoError(t, err, "search %s", search)

		return res, buf.String()
	}

	wantRes, want := bundle(filepath.Join(dir, "vendor"))
	res, got := bundle(filepath.Join(dir, "vlink"))

	require.Equal(t, []string{filepath.Join(dir, "src", "main.lua"), filepath.Join(dir, "vendor", "v.lua")},
		wantRes.Files, "the files of the resolved spelling")
	assert.Equal(t, wantRes.Files, res.Files, "the files")
	assert.Equal(t, want, got, "the bundles")
}
