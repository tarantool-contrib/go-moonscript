package resolve_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/resolve"
)

func TestResolver(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Create directory structure.
	luaDir := filepath.Join(tmpDir, "lua")
	require.NoError(t, os.MkdirAll(luaDir, 0750))

	libDir := filepath.Join(tmpDir, "lib")
	require.NoError(t, os.MkdirAll(libDir, 0750))

	// Write test Lua files.
	fooPath := filepath.Join(luaDir, "foo.lua")
	require.NoError(t, os.WriteFile(fooPath, []byte("-- foo"), 0600))

	barInitPath := filepath.Join(libDir, "bar", "init.lua")
	require.NoError(t, os.MkdirAll(filepath.Dir(barInitPath), 0750))
	require.NoError(t, os.WriteFile(barInitPath, []byte("-- bar"), 0600))

	resolver := resolve.New(luaDir, []string{libDir}, "?.lua;?/init.lua")

	tests := []struct {
		name     string
		require  string
		fromDir  string
		wantPath string
		wantName string
	}{
		{
			name:     "simple module",
			require:  "foo",
			fromDir:  luaDir,
			wantPath: fooPath,
			wantName: "foo",
		},
		{
			name:     "init.lua module",
			require:  "bar",
			fromDir:  libDir,
			wantPath: barInitPath,
			wantName: "bar",
		},
		{
			name:     "dot notation",
			require:  "foo.bar",
			fromDir:  luaDir,
			wantPath: "", // not found.
			wantName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := resolver.Resolve(tt.require, tt.fromDir)
			if tt.wantPath == "" {
				assert.Error(t, err, "Resolve() expected error, got result %v", result)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, result.FilePath)
			assert.Equal(t, tt.wantName, result.ModName)
		})
	}
}

func TestNormalizeRequireName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"foo", "foo"},
		{"foo.bar", "foo/bar"},
		{"foo.bar.baz", "foo/bar/baz"},
		{"./foo", "./foo"},
		{"foo/bar", "foo/bar"},
		{"../foo", "../foo"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			got := resolve.NormalizeRequireName(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestResolverWithPrefix(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Create root directory with hyphenated name for auto-detection test.
	rootDir := filepath.Join(tmpDir, "test-project")
	require.NoError(t, os.MkdirAll(rootDir, 0750))

	// Create lib directory inside root.
	libDir := filepath.Join(rootDir, "lib")
	require.NoError(t, os.MkdirAll(libDir, 0750))

	// Write test Lua file: lib/tuple_config.lua.
	tupleConfigPath := filepath.Join(libDir, "tuple_config.lua")
	require.NoError(t, os.WriteFile(tupleConfigPath, []byte("-- tuple config"), 0600))

	// Test with explicit strip prefix.
	resolver := resolve.NewWithPrefix(rootDir, []string{libDir}, "?.lua;?/init.lua", "tuple_diff")

	tests := []struct {
		name     string
		require  string
		fromDir  string
		wantPath string
		wantName string
	}{
		{
			name:     "strip explicit prefix",
			require:  "tuple_diff.lib.tuple_config",
			fromDir:  rootDir,
			wantPath: tupleConfigPath,
			wantName: "tuple_diff.lib.tuple_config",
		},
		{
			name:     "strip auto-detected prefix from root directory",
			require:  "test_project.lib.tuple_config",
			fromDir:  rootDir,
			wantPath: tupleConfigPath,
			wantName: "test_project.lib.tuple_config",
		},
		{
			name:     "strip lib prefix when searching in lib directory",
			require:  "lib.tuple_config",
			fromDir:  libDir,
			wantPath: tupleConfigPath,
			wantName: "lib.tuple_config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := resolver.Resolve(tt.require, tt.fromDir)
			if tt.wantPath == "" {
				assert.Error(t, err, "Resolve() expected error, got result %v", result)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, result.FilePath)
			assert.Equal(t, tt.wantName, result.ModName)
		})
	}
}
