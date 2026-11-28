package luamerge_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge"
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

// TestLoadOptions_PackageNameOverride checks that Options.PackageName set
// after LoadOptions is the one the bundle uses. LoadOptions used to apply the
// file's package_name to StripPrefix and PackagePrefix, which Bundle then
// kept, so the bundle went on using the file's name.
func TestLoadOptions_PackageNameOverride(t *testing.T) {
	t.Parallel()

	// Arrange: modules named lib.* in a flat layout.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"), []byte("print(require(\"lib.util\"))\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "util.lua"), []byte("return 1\n"), 0o600))

	path := filepath.Join(dir, "luamerge.yaml")
	require.NoError(t, os.WriteFile(path, []byte("package_name: other\n"), 0o600))

	opts, err := luamerge.LoadOptions(path)
	require.NoError(t, err)

	opts.Entry = filepath.Join(dir, "main.lua")
	opts.PackageName = "lib"
	opts.Strict = true

	// Act.
	var buf bytes.Buffer

	_, err = luamerge.Bundle(opts, &buf)

	// Assert: lib.util resolves in strict mode only by stripping lib.
	require.NoError(t, err)
	assert.Contains(t, buf.String(), `package.preload["lib.util"]`)
	assert.NotContains(t, buf.String(), "other.")
}

// TestLoadOptions_ArgFix checks that arg_fix in the file reaches
// Options.NoArgFix, its inverse: the arg line is on unless arg_fix is false.
func TestLoadOptions_ArgFix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		file     string
		noArgFix bool
	}{
		{name: "default", file: "# no settings\n", noArgFix: false},
		{name: "on", file: "arg_fix: true\n", noArgFix: false},
		{name: "off", file: "arg_fix: false\n", noArgFix: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			path := filepath.Join(t.TempDir(), "luamerge.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.file), 0o600))

			// Act.
			opts, err := luamerge.LoadOptions(path)

			// Assert.
			require.NoError(t, err)
			assert.Equal(t, tt.noArgFix, opts.NoArgFix)
		})
	}
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
