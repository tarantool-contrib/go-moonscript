package resolve_test

import (
	"os"
	"path/filepath"
	"strings"
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
		wantPath string
		wantName string
	}{
		{
			name:     "simple module",
			require:  "foo",
			wantPath: fooPath,
			wantName: "foo",
		},
		{
			name:     "init.lua module",
			require:  "bar",
			wantPath: barInitPath,
			wantName: "bar",
		},
		{
			name:     "dot notation",
			require:  "foo.bar",
			wantPath: "", // not found.
			wantName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := resolver.Resolve(tt.require)
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

	// As in package.searchpath, every "." becomes a separator, in a name
	// written as a path too: no ".." survives.
	tests := []struct {
		input    string
		expected string
	}{
		{"foo", "foo"},
		{"foo.bar", "foo/bar"},
		{"foo.bar.baz", "foo/bar/baz"},
		{"./foo", "//foo"},
		{"foo/bar", "foo/bar"},
		{"../foo", "///foo"},
		{".foo", "/foo"},
		{"a/b.c", "a/b/c"},
		{"foo.", "foo/"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			got := resolve.NormalizeRequireName(tt.input)
			assert.Equal(t, filepath.FromSlash(tt.expected), got)
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

	// A search directory outside the root: vendor/json.lua.
	vendorDir := filepath.Join(tmpDir, "deps", "vendor")
	require.NoError(t, os.MkdirAll(vendorDir, 0750))

	jsonPath := filepath.Join(vendorDir, "json.lua")
	require.NoError(t, os.WriteFile(jsonPath, []byte("-- json"), 0600))

	// Test with explicit strip prefix.
	resolver := resolve.NewWithPrefix(rootDir, []string{libDir, vendorDir}, "?.lua;?/init.lua", "tuple_diff")

	tests := []struct {
		name     string
		require  string
		wantPath string
		wantName string
	}{
		{
			name:     "strip explicit prefix",
			require:  "tuple_diff.lib.tuple_config",
			wantPath: tupleConfigPath,
			wantName: "tuple_diff.lib.tuple_config",
		},
		{
			name:     "strip auto-detected prefix from root directory",
			require:  "test_project.lib.tuple_config",
			wantPath: tupleConfigPath,
			wantName: "test_project.lib.tuple_config",
		},
		{
			name:     "search directory below the root found natively",
			require:  "lib.tuple_config",
			wantPath: tupleConfigPath,
			wantName: "lib.tuple_config",
		},
		{
			name:     "strip vendor prefix when searching in vendor directory",
			require:  "vendor.json",
			wantPath: jsonPath,
			wantName: "vendor.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := resolver.Resolve(tt.require)
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

// testWriteFiles creates each file (a slash path relative to dir) with a
// one-line module.
func testWriteFiles(t *testing.T, dir string, files ...string) {
	t.Helper()

	for _, rel := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte("return true\n"), 0o600))
	}
}

// TestResolve_ExtensionsOnlyWhenNativeLookupFails checks that the name
// extensions (the strip prefix, the root directory's name, a search
// directory's name) apply only once the lookup Lua itself does has found
// nothing at any location: a file Lua would load always wins, even one in a
// later search directory over an extension match at the root.
func TestResolve_ExtensionsOnlyWhenNativeLookupFails(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testWriteFiles(t, tmpDir,
		"my-app/mylib/util.lua", "my-app/util.lua",
		"my-app/lib/mylib/conf.lua", "my-app/conf.lua",
		"my-app/only.lua",
		"my-app/my_app/x.lua", "my-app/x.lua",
		"my-app/y.lua",
		"deps/vendor/vendor/z.lua", "deps/vendor/z.lua",
		"deps/vendor/q.lua", "my-app/lib/vendor/q.lua",
		"deps/vendor/json.lua",
	)

	root := filepath.Join(tmpDir, "my-app")
	searchDirs := []string{filepath.Join(tmpDir, "deps", "vendor"), filepath.Join(root, "lib")}
	resolver := resolve.NewWithPrefix(root, searchDirs, "?.lua;?/init.lua", "mylib")

	tests := []struct {
		name    string
		require string
		want    string
	}{
		{name: "strip prefix: native file wins", require: "mylib.util", want: "my-app/mylib/util.lua"},
		{name: "strip prefix: search dir file wins", require: "mylib.conf", want: "my-app/lib/mylib/conf.lua"},
		{name: "strip prefix: fallback", require: "mylib.only", want: "my-app/only.lua"},
		{name: "root name: native file wins", require: "my_app.x", want: "my-app/my_app/x.lua"},
		{name: "root name: fallback", require: "my_app.y", want: "my-app/y.lua"},
		{name: "search dir name: native file wins", require: "vendor.z", want: "deps/vendor/vendor/z.lua"},
		{name: "search dir name: later search dir wins", require: "vendor.q", want: "my-app/lib/vendor/q.lua"},
		{name: "search dir name: fallback", require: "vendor.json", want: "deps/vendor/json.lua"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := resolver.Resolve(tt.require)

			require.NoError(t, err)
			assert.Equal(t, filepath.Join(tmpDir, filepath.FromSlash(tt.want)), result.FilePath)
			assert.Equal(t, tt.require, result.ModName)
		})
	}
}

// TestResolve_PathStyleNamesStayInside checks that a name written as a path
// cannot reach outside the root or a search directory: as in
// package.searchpath, every "." becomes a separator, so "../foo" names foo in
// each location searched, never the foo.lua next to the project.
func TestResolve_PathStyleNamesStayInside(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testWriteFiles(t, tmpDir, "foo.lua", "outside.lua", "proj/foo.lua", "proj/lib/x.lua")

	root := filepath.Join(tmpDir, "proj")
	resolver := resolve.New(root, []string{filepath.Join(root, "lib")}, "?.lua;?/init.lua")

	for name, want := range map[string]string{
		"../foo":    "proj/foo.lua",
		"../../foo": "proj/foo.lua",
		"./foo":     "proj/foo.lua",
		".foo":      "proj/foo.lua",
		"../x":      "proj/lib/x.lua",
	} {
		result, err := resolver.Resolve(name)
		require.NoError(t, err, "Resolve(%q)", name)
		assert.Equal(t, filepath.Join(tmpDir, filepath.FromSlash(want)), result.FilePath, "Resolve(%q)", name)
	}

	result, err := resolver.Resolve("../outside")
	require.ErrorIs(t, err, resolve.ErrModuleNotFound, "Resolve(\"../outside\") = %v", result)
}

// TestResolve_NotFound checks the error for a name found nowhere: it wraps
// ErrModuleNotFound and lists every candidate in the order tried, the native
// ones at every location before the extension ones.
func TestResolve_NotFound(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "proj")
	lib := filepath.Join(root, "lib")
	require.NoError(t, os.MkdirAll(lib, 0o750))

	resolver := resolve.NewWithPrefix(root, []string{lib}, "?.lua;?/init.lua", "mylib")

	result, err := resolver.Resolve("mylib.nope")

	require.ErrorIs(t, err, resolve.ErrModuleNotFound)
	assert.Nil(t, result)

	_, list, found := strings.Cut(err.Error(), "; tried:\n  ")
	require.True(t, found, "the error lists the candidates: %v", err)

	list = strings.TrimSuffix(list, ": "+resolve.ErrModuleNotFound.Error())
	assert.Equal(t, []string{
		filepath.Join(root, "mylib", "nope.lua"),
		filepath.Join(root, "mylib", "nope", "init.lua"),
		filepath.Join(lib, "mylib", "nope.lua"),
		filepath.Join(lib, "mylib", "nope", "init.lua"),
		filepath.Join(root, "nope.lua"),
		filepath.Join(root, "nope", "init.lua"),
		filepath.Join(lib, "nope.lua"),
		filepath.Join(lib, "nope", "init.lua"),
	}, strings.Split(list, "\n  "))
}

// TestResolve_AbsolutePathTemplate reproduces the review's own repro: a
// template given as an absolute path resolves the file it names, the way
// LUA_PATH="$PWD/lib/?.lua" does natively. Before the fix, the absolute
// template was joined under the root like a relative one, doubling the root
// into the candidate path so the file was never found.
func TestResolve_AbsolutePathTemplate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lib"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "lib", "m.lua"), []byte("return \"LIB\"\n"), 0o600))

	resolver := resolve.New(root, nil, filepath.Join(root, "lib", "?.lua"))

	result, err := resolver.Resolve("m")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "lib", "m.lua"), result.FilePath)
}

// TestResolve_AbsoluteTemplateTriedOnce checks that an absolute template
// names the same file no matter how many locations Resolve otherwise
// searches (the root and every --search directory): it must not be tried
// once per location, which would still be wrong even where it happens to
// resolve, because the "tried" error would blame the wrong number of
// candidates and a slower, repeated stat of the same absolute file.
func TestResolve_AbsoluteTemplateTriedOnce(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	libDir := filepath.Join(tmpDir, "lib")
	require.NoError(t, os.MkdirAll(libDir, 0o750))

	root := filepath.Join(tmpDir, "proj")
	require.NoError(t, os.MkdirAll(root, 0o750))

	search := filepath.Join(tmpDir, "vendor")
	require.NoError(t, os.MkdirAll(search, 0o750))

	resolver := resolve.New(root, []string{search}, filepath.Join(libDir, "?.lua"))

	_, err := resolver.Resolve("missing")

	require.ErrorIs(t, err, resolve.ErrModuleNotFound)

	tried := strings.Count(err.Error(), filepath.Join(libDir, "missing.lua"))
	assert.Equal(t, 1, tried, "the absolute template must be tried once, not once per location: %v", err)
}

// TestResolve_EmptyTemplateEntryUsesDefault checks that an empty entry in
// --path — from a leading, trailing or doubled ";" — expands to the default
// templates ?.lua;?/init.lua in place, the same convention LUA_PATH's own
// ";;" uses for "the default path here", instead of being silently dropped.
func TestResolve_EmptyTemplateEntryUsesDefault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	resolver := resolve.New(root, nil, "custom/?.lua;;other/?.lua")

	result, err := resolver.Resolve("m")

	require.ErrorIs(t, err, resolve.ErrModuleNotFound)
	assert.Nil(t, result)

	_, list, found := strings.Cut(err.Error(), "; tried:\n  ")
	require.True(t, found, "the error lists the candidates: %v", err)

	list = strings.TrimSuffix(list, ": "+resolve.ErrModuleNotFound.Error())
	assert.Equal(t, []string{
		filepath.Join(root, "custom", "m.lua"),
		filepath.Join(root, "m.lua"),
		filepath.Join(root, "m", "init.lua"),
		filepath.Join(root, "other", "m.lua"),
	}, strings.Split(list, "\n  "))
}
