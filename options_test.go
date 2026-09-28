package luamerge_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge"
)

// TestLoadOptions_MissingNamedFile checks that a configuration file named
// explicitly must exist.
func TestLoadOptions_MissingNamedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "luamerge.yml")

	_, err := luamerge.LoadOptions(path)

	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Contains(t, err.Error(), path)
}

// TestLoadOptions_MissingDefaultFile checks that without a path the default
// file is optional: the defaults apply.
//
//nolint:paralleltest // t.Chdir changes the working directory of the whole process.
func TestLoadOptions_MissingDefaultFile(t *testing.T) {
	t.Chdir(t.TempDir())

	opts, err := luamerge.LoadOptions("")

	require.NoError(t, err)
	assert.Equal(t, luamerge.DefaultOptions(), opts)
}

// TestLoadOptions_NamedFile checks that the file named is read.
func TestLoadOptions_NamedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "build.yaml")
	require.NoError(t, os.WriteFile(path, []byte("entry: src/main.lua\nstrict: true\n"), 0o600))

	opts, err := luamerge.LoadOptions(path)

	require.NoError(t, err)
	assert.Equal(t, "src/main.lua", opts.Entry)
	assert.True(t, opts.Strict)
}
