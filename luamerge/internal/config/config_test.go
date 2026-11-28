package config_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	assert.Equal(t, "-", cfg.Output, "Default().Output = %q, want \"-\"", cfg.Output)
	assert.Equal(t, "?.lua;?/init.lua", cfg.Path, "Default().Path = %q, want %q", cfg.Path, "?.lua;?/init.lua")
	assert.False(t, cfg.Strict, "Default().Strict = %v, want false", cfg.Strict)
	assert.Empty(t, cfg.Search, "len(Default().Search) = %d, want 0", len(cfg.Search))
}

// TestLoadConfig_MissingNamedFile checks that a configuration file named
// explicitly must exist: a typo in its name used to drop every setting in the
// file without a word.
func TestLoadConfig_MissingNamedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "luamerge.yml")

	_, err := config.LoadConfig(path)

	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Contains(t, err.Error(), path)
}

// TestLoadConfig_MissingDefaultFile checks that the default file, unlike a
// named one, may be absent: then the defaults apply.
//
// Not parallel: t.Chdir changes the working directory of the whole process.
func TestLoadConfig_MissingDefaultFile(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, err := config.LoadConfig("")

	require.NoError(t, err)
	assert.Equal(t, config.Default(), cfg)
}

// TestLoadConfig_DefaultFile checks that an empty path reads DefaultPath from
// the working directory.
//
// Not parallel: t.Chdir changes the working directory of the whole process.
func TestLoadConfig_DefaultFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.DefaultPath), []byte("strict: true\n"), 0o600))
	t.Chdir(dir)

	cfg, err := config.LoadConfig("")

	require.NoError(t, err)
	assert.True(t, cfg.Strict, "the default file was not read")
}

// TestLoadConfig_NoSettings checks that a config file holding no settings,
// whether it is empty, blank or only comments, gives the defaults. go-config
// refuses an empty file ("no data to process"), which made an empty
// luamerge.yaml, or --config /dev/null, an error.
func TestLoadConfig_NoSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"blank lines", "\n  \n\n"},
		{"comments", "# no settings yet\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "luamerge.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o600))

			cfg, err := config.LoadConfig(path)

			require.NoError(t, err)
			assert.Equal(t, config.Default(), cfg)
		})
	}

	t.Run("null device", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.LoadConfig(os.DevNull)

		require.NoError(t, err)
		assert.Equal(t, config.Default(), cfg)
	})
}

// TestLoadConfig_NoSettingsWithEnvironment checks that the environment still
// applies over a config file holding no settings.
func TestLoadConfig_NoSettingsWithEnvironment(t *testing.T) {
	// t.Setenv forbids t.Parallel().
	t.Setenv("LUAMERGE_STRICT", "true")

	for _, content := range []string{"", "# no settings yet\n"} {
		path := filepath.Join(t.TempDir(), "luamerge.yaml")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		cfg, err := config.LoadConfig(path)

		require.NoError(t, err, "content %q", content)

		want := config.Default()
		want.Strict = true
		assert.Equal(t, want, cfg, "content %q", content)
	}
}

// TestLoadConfig_EnvironmentLists checks that a list-valued setting is taken
// from the environment as a comma-separated value, replacing the list of the
// config file. go-config passed the value on as one string, which the schema
// refused.
func TestLoadConfig_EnvironmentLists(t *testing.T) {
	// t.Setenv forbids t.Parallel().
	path := filepath.Join(t.TempDir(), "luamerge.yaml")
	require.NoError(t, os.WriteFile(path,
		[]byte("search: [src]\nskip_packages: [yaml]\ninclude_packages: [plugin]\n"), 0o600))

	t.Setenv("LUAMERGE_SEARCH", "lib,vendor")
	t.Setenv("LUAMERGE_SKIP_PACKAGES", "cjson, socket ,xlog.*")
	t.Setenv("LUAMERGE_INCLUDE_PACKAGES", "")

	cfg, err := config.LoadConfig(path)

	require.NoError(t, err)
	assert.Equal(t, []string{"lib", "vendor"}, cfg.Search)
	assert.Equal(t, []string{"cjson", "socket", "xlog.*"}, cfg.SkipPackages)
	assert.Equal(t, []string{}, cfg.IncludePackages, "an empty value is the empty list")
}

// TestLoadConfig_IgnoresUnprefixedVariables checks that only variables with
// the LUAMERGE_ prefix are configuration: a variable named like a list
// setting, in any case, sets nothing.
func TestLoadConfig_IgnoresUnprefixedVariables(t *testing.T) {
	// t.Setenv forbids t.Parallel().
	t.Setenv("SEARCH", "lib")
	t.Setenv("SKIP_PACKAGES", "cjson")
	t.Setenv("include_packages", "plugin")

	// A named file, so that no luamerge.yaml of the working directory is read.
	path := filepath.Join(t.TempDir(), "luamerge.yaml")
	require.NoError(t, os.WriteFile(path, []byte("# no settings\n"), 0o600))

	cfg, err := config.LoadConfig(path)

	require.NoError(t, err)
	assert.Equal(t, config.Default(), cfg)
}

// TestSplitList checks how a comma-separated list value is split.
func TestSplitList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  []string
	}{
		{"", []string{}},
		{"lib", []string{"lib"}},
		{"lib,vendor", []string{"lib", "vendor"}},
		{" lib , vendor ", []string{"lib", "vendor"}},
		{"lib,,vendor,", []string{"lib", "vendor"}},
		{",", []string{}},
		{"xlog.*", []string{"xlog.*"}},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, config.SplitList(tt.value), "SplitList(%q)", tt.value)
	}
}

// TestFilePath checks the name of the file LoadConfig reads for a path.
func TestFilePath(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "luamerge.yaml", config.FilePath(""))
	assert.Equal(t, "build/luamerge.yaml", config.FilePath("build/luamerge.yaml"))
}

func TestLoadConfigValidYAML(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "luamerge.yaml")
	yamlContent := `entry: src/main.lua
output: dist/bundle.lua
root: src/
path: "?.lua"
search:
  - lib/
  - vendor/
strict: true
prefix: |
  print("prefix")
suffix: |
  print("suffix")
transform:
  remove_comments: true
  remove_empty_lines: false
  minify: false
`
	require.NoError(t, os.WriteFile(configPath, []byte(yamlContent), 0600))

	cfg, err := config.LoadConfig(configPath)
	require.NoError(t, err)

	assert.Equal(t, "src/main.lua", cfg.Entry, "Entry = %q, want %q", cfg.Entry, "src/main.lua")
	assert.Equal(t, "dist/bundle.lua", cfg.Output, "Output = %q, want %q", cfg.Output, "dist/bundle.lua")
	assert.Equal(t, "src/", cfg.Root, "Root = %q, want %q", cfg.Root, "src/")
	assert.Equal(t, "?.lua", cfg.Path, "Path = %q, want %q", cfg.Path, "?.lua")

	wantSearch := []string{"lib/", "vendor/"}
	assert.Equal(t, wantSearch, cfg.Search, "Search = %v, want %v", cfg.Search, wantSearch)
	assert.True(t, cfg.Strict, "Strict = false, want true")
	assert.True(t, cfg.Transform.RemoveComments, "Transform.RemoveComments = false, want true")
	assert.False(t, cfg.Transform.RemoveEmptyLines, "Transform.RemoveEmptyLines = true, want false")
	assert.False(t, cfg.Transform.Minify, "Transform.Minify = true, want false")
	assert.Equal(t, "print(\"prefix\")\n", cfg.Prefix, "Prefix = %q, want %q", cfg.Prefix, "print(\"prefix\")\\n")
	assert.Equal(t, "print(\"suffix\")\n", cfg.Suffix, "Suffix = %q, want %q", cfg.Suffix, "print(\"suffix\")\\n")
}

func TestLoadConfigSchemaRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "luamerge.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("entry: main.lua\nbogus_key: 1\n"), 0600))

	_, err := config.LoadConfig(configPath)
	require.ErrorContains(t, err, "bogus_key [schema]", "unknown config key should fail schema validation")
}

// TestLoadConfig_RejectsStripShebang: transform.strip_shebang is gone, like
// the --strip-shebang flag, and an unknown key is an error, in the file and
// in the environment.
func TestLoadConfig_RejectsStripShebang(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "luamerge.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("transform:\n  strip_shebang: true\n"), 0600))

	_, err := config.LoadConfig(configPath)
	require.ErrorContains(t, err, "strip_shebang", "the key in the file")

	// t.Setenv forbids t.Parallel().
	t.Setenv("LUAMERGE_TRANSFORM_STRIP_SHEBANG", "true")

	// A named config file must exist, so the environment is checked over a
	// file that holds no settings of its own.
	commentOnly := filepath.Join(t.TempDir(), "luamerge.yaml")
	require.NoError(t, os.WriteFile(commentOnly, []byte("# no settings\n"), 0600))

	_, err = config.LoadConfig(commentOnly)
	require.ErrorContains(t, err, "strip_shebang", "the key in the environment")
}

func TestLoadConfigSchemaRejectsBadBoolString(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "luamerge.yaml")
	// "yes" is not accepted by strconv.ParseBool, so the schema must reject it,
	// before the value is ever decoded into a bool.
	require.NoError(t, os.WriteFile(configPath, []byte(`strict: "yes"`+"\n"), 0600))

	_, err := config.LoadConfig(configPath)
	require.ErrorContains(t, err, "strict [type]", "non-parseable bool string should fail schema validation")
}

func TestLoadConfigSchemaRejectsListAsScalar(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "luamerge.yaml")
	// A list field must be an array; a bare scalar is rejected.
	require.NoError(t, os.WriteFile(configPath, []byte("skip_packages: cjson\n"), 0600))

	_, err := config.LoadConfig(configPath)
	require.ErrorContains(t, err, "skip_packages [type]", "scalar for a list field should fail schema validation")
}

// TestLoadConfig_RejectsNonStringListItems checks that the items of a list
// setting must be strings: a number, a map or a null in the list is an error,
// not a module named "1" or "map[a:1]".
func TestLoadConfig_RejectsNonStringListItems(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"search", "skip_packages", "include_packages"} {
		for _, list := range []string{"[1]", "[lib, {a: 1}]", "[null]"} {
			t.Run(key+" "+list, func(t *testing.T) {
				t.Parallel()

				configPath := filepath.Join(t.TempDir(), "luamerge.yaml")
				require.NoError(t, os.WriteFile(configPath, []byte(key+": "+list+"\n"), 0o600))

				_, err := config.LoadConfig(configPath)

				require.ErrorContains(t, err, key+" [items]")
			})
		}
	}
}

// TestLoadConfig_RejectsEmptyOutputAndPath checks that output and path may
// not be empty in the configuration: an empty output names no file, and an
// empty path no template.
func TestLoadConfig_RejectsEmptyOutputAndPath(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"output", "path"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			configPath := filepath.Join(t.TempDir(), "luamerge.yaml")
			require.NoError(t, os.WriteFile(configPath, []byte(key+": \"\"\n"), 0o600))

			_, err := config.LoadConfig(configPath)

			require.ErrorContains(t, err, key+" [minLength]")
		})
	}
}

func TestLoadConfigSchemaAcceptsBoolStringForms(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "luamerge.yaml")
	// Native bool and every strconv.ParseBool string form must be accepted.
	require.NoError(t, os.WriteFile(configPath, []byte("strict: true\ndebug: \"T\"\nfallback: \"0\"\n"), 0600))

	cfg, err := config.LoadConfig(configPath)
	require.NoError(t, err)
	assert.True(t, cfg.Strict)
	assert.True(t, cfg.Debug)
	assert.False(t, cfg.Fallback)
}

// TestSchemaDefaultsMatchDefault guards against drift between the `default`
// annotations in schema.json and config.Default(). The schema defaults are
// documentation for tooling; go-config does not inject them, so this test is
// the only thing keeping the two in sync.
func TestSchemaDefaultsMatchDefault(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("schema.json")
	require.NoError(t, err)

	type property struct {
		Default    any                 `json:"default"`
		Properties map[string]property `json:"properties"`
	}

	var schema struct {
		Properties map[string]property `json:"properties"`
	}

	require.NoError(t, json.Unmarshal(raw, &schema))

	def := config.Default()

	top := map[string]any{
		"entry":            def.Entry,
		"output":           def.Output,
		"root":             def.Root,
		"path":             def.Path,
		"search":           def.Search,
		"strict":           def.Strict,
		"debug":            def.Debug,
		"fallback":         def.Fallback,
		"arg_fix":          def.ArgFix,
		"prefix":           def.Prefix,
		"suffix":           def.Suffix,
		"shebang":          def.Shebang,
		"package_prefix":   def.PackagePrefix,
		"package_name":     def.PackageName,
		"strip_prefix":     def.StripPrefix,
		"skip_packages":    def.SkipPackages,
		"include_packages": def.IncludePackages,
	}
	for key, want := range top {
		prop, ok := schema.Properties[key]
		require.Truef(t, ok, "schema missing property %q", key)
		assertSchemaDefault(t, key, want, prop.Default)
	}

	transform := schema.Properties["transform"]

	nested := map[string]any{
		"remove_comments":    def.Transform.RemoveComments,
		"remove_empty_lines": def.Transform.RemoveEmptyLines,
		"minify":             def.Transform.Minify,
	}
	for key, want := range nested {
		prop, ok := transform.Properties[key]
		require.Truef(t, ok, "schema missing transform property %q", key)
		assertSchemaDefault(t, "transform."+key, want, prop.Default)
	}
}

// assertSchemaDefault compares a schema `default` (decoded from JSON, so lists
// arrive as []any) against the corresponding config.Default() field value.
func assertSchemaDefault(t *testing.T, key string, want, got any) {
	t.Helper()

	if wantSlice, ok := want.([]string); ok {
		gotSlice, ok := got.([]any)
		require.Truef(t, ok, "%s: schema default is not an array", key)

		asStrings := make([]string, len(gotSlice))
		for i, elem := range gotSlice {
			asStrings[i] = elem.(string) //nolint:forcetypeassert // schema items are strings.
		}

		assert.Equalf(t, wantSlice, asStrings, "%s default", key)

		return
	}

	assert.Equalf(t, want, got, "%s default", key)
}

func TestLoadConfigSchemaRejectsWrongType(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "luamerge.yaml")
	// entry must be a string, not a list.
	require.NoError(t, os.WriteFile(configPath, []byte("entry:\n  - a\n  - b\n"), 0600))

	_, err := config.LoadConfig(configPath)
	require.ErrorContains(t, err, "entry [type]", "wrong-typed config value should fail schema validation")
}

func TestLoadConfigWithStripPrefix(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "luamerge.yaml")
	yamlContent := `entry: src/main.lua
strip_prefix: "tuple_diff"
package_prefix: "mypkg"
`
	require.NoError(t, os.WriteFile(configPath, []byte(yamlContent), 0600))

	cfg, err := config.LoadConfig(configPath)
	require.NoError(t, err)

	assert.Equal(t, "tuple_diff", cfg.StripPrefix, "StripPrefix = %q, want %q", cfg.StripPrefix, "tuple_diff")
	assert.Equal(t, "mypkg", cfg.PackagePrefix, "PackagePrefix = %q, want %q", cfg.PackagePrefix, "mypkg")
}

// canonicalTempDir returns a new temporary directory spelled without symbolic
// links: on macOS t.TempDir() is under /var, a link to /private/var.
func canonicalTempDir(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	return dir
}

// TestResolveRoot_Spellings checks that however the entry and the root are
// spelled, relative, absolute, through a symbolic link, ResolveRoot turns
// them into the same absolute paths without links. Module names are paths
// relative to the root, so a root spelled "src" named the entry "main"
// instead of "app.main", and one reached through a link gave dotted "..".
//
//nolint:paralleltest // t.Chdir changes the working directory of the whole process.
func TestResolveRoot_Spellings(t *testing.T) {
	dir := canonicalTempDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src", "app"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "app", "main.lua"), nil, 0o600))
	require.NoError(t, os.Symlink(".", filepath.Join(dir, "link")))
	t.Chdir(dir)

	wantRoot := filepath.Join(dir, "src")
	wantEntry := filepath.Join(dir, "src", "app", "main.lua")

	tests := []struct {
		name  string
		entry string
		root  string
		want  string // The root expected.
	}{
		{"relative root", "src/app/main.lua", "src", wantRoot},
		{"relative root with a slash", "src/app/main.lua", "src/", wantRoot},
		{"unclean spelling", "./src/../src/app/main.lua", "./src/.", wantRoot},
		{"absolute root", "src/app/main.lua", wantRoot, wantRoot},
		{"root through a link", "src/app/main.lua", filepath.Join(dir, "link", "src"), wantRoot},
		{"entry through a link", filepath.Join("link", "src", "app", "main.lua"), "src", wantRoot},
		{"root derived from the entry", "src/app/main.lua", "", filepath.Join(dir, "src", "app")},
		{"root derived through a link", filepath.Join("link", "src", "app", "main.lua"), "",
			filepath.Join(dir, "src", "app")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Entry = tt.entry
			cfg.Root = tt.root

			require.NoError(t, cfg.ResolveRoot())

			assert.Equal(t, tt.want, cfg.Root, "root")
			assert.Equal(t, wantEntry, cfg.Entry, "entry")
		})
	}
}

// TestResolveRoot_SearchSpellings checks that however a search directory is
// spelled, relative, absolute, through a symbolic link or from a working
// directory reached through one, ResolveRoot turns it into the same absolute
// path without links, as it does the root. A module's path is its search
// directory's joined with the file's, so a directory spelled through a link
// gave its modules paths through the link, and chunk names from the root
// that climbed out to the link. The list the caller passed is left as it
// was.
//
// Not parallel: t.Chdir changes the working directory of the whole process.
func TestResolveRoot_SearchSpellings(t *testing.T) {
	dir := canonicalTempDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "vendor"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "main.lua"), nil, 0o600))
	require.NoError(t, os.Symlink("vendor", filepath.Join(dir, "vlink")))
	require.NoError(t, os.Symlink(".", filepath.Join(dir, "link")))

	want := []string{filepath.Join(dir, "vendor"), filepath.Join(dir, "src")}

	tests := []struct {
		name   string
		search string
		// wd is the working directory, relative to dir.
		wd string
	}{
		{name: "relative", search: "vendor", wd: "."},
		{name: "unclean", search: "./src/../vendor/", wd: "."},
		{name: "absolute", search: filepath.Join(dir, "vendor"), wd: "."},
		{name: "through a link", search: "vlink", wd: "."},
		{name: "under a link", search: filepath.Join("link", "vendor"), wd: "."},
		{name: "absolute through a link", search: filepath.Join(dir, "link", "vlink"), wd: "."},
		{name: "from a working directory through a link", search: "vendor", wd: "link"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Also sets PWD, which os.Getwd prefers: the spelling of the
			// working directory through the link, as a shell keeps it.
			t.Chdir(filepath.Join(dir, tt.wd))

			search := []string{tt.search, "src"}
			cfg := config.Default()
			cfg.Entry = filepath.Join("src", "main.lua")
			cfg.Search = search

			require.NoError(t, cfg.ResolveRoot())

			assert.Equal(t, want, cfg.Search, "search")
			assert.Equal(t, []string{tt.search, "src"}, search, "the caller's list is left as it was")
		})
	}
}

// TestResolveRoot_Missing checks that an entry, a root or a search directory
// that does not exist is an error, rather than a build under a root or with
// a search directory nothing can be found in.
func TestResolveRoot_Missing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	entry := filepath.Join(dir, "main.lua")
	require.NoError(t, os.WriteFile(entry, nil, 0o600))

	tests := []struct {
		name   string
		entry  string
		root   string
		search []string
	}{
		{"entry", filepath.Join(dir, "missing.lua"), "", nil},
		{"root", entry, filepath.Join(dir, "src"), nil},
		{"search", entry, "", []string{dir, filepath.Join(dir, "lib")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Default()
			cfg.Entry = tt.entry
			cfg.Root = tt.root
			cfg.Search = tt.search

			err := cfg.ResolveRoot()

			require.ErrorIs(t, err, fs.ErrNotExist)
			assert.Contains(t, err.Error(), tt.name)
		})
	}
}

func TestResolveRoot(t *testing.T) {
	t.Parallel()

	tmpDir := canonicalTempDir(t)
	entryPath := filepath.Join(tmpDir, "src", "main.lua")
	require.NoError(t, os.MkdirAll(filepath.Dir(entryPath), 0750))
	require.NoError(t, os.WriteFile(entryPath, nil, 0o600))

	cfg := config.Config{
		Entry:    entryPath,
		Output:   "",
		Root:     "", // Root not set.
		Path:     "",
		Search:   nil,
		Strict:   false,
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    nil,
		IncludePackages: nil,
	}
	require.NoError(t, cfg.ResolveRoot())

	expectedRoot := filepath.Dir(entryPath)
	assert.Equal(t, expectedRoot, cfg.Root, "Root = %q, want %q", cfg.Root, expectedRoot)
}

func TestResolveRootWithEntryMissing(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Entry:    "",
		Output:   "",
		Root:     "",
		Path:     "",
		Search:   nil,
		Strict:   false,
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    nil,
		IncludePackages: nil,
	}
	err := cfg.ResolveRoot()
	require.Error(t, err, "ResolveRoot() expected error")
}

func TestShouldSkip(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Entry:    "",
		Output:   "",
		Root:     "",
		Path:     "",
		Search:   nil,
		Strict:   false,
		Debug:    false,
		Fallback: false,
		ArgFix:   false,
		Transform: config.TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    []string{"xlog.*", "yaml", "cjson"},
		IncludePackages: nil,
	}

	tests := []struct {
		name     string
		expected bool
	}{
		{"xlog.core", true},
		{"xlog.util", true},
		{"xlog", true}, // prefix match without dot also matches.
		{"xlogextra", false},
		{"yaml", true},
		{"yaml.util", false},
		{"cjson", true},
		{"json", false},
		{"other", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := cfg.ShouldSkip(tt.name)
			assert.Equal(t, tt.expected, got, "ShouldSkip(%q) = %v, want %v", tt.name, got, tt.expected)
		})
	}
}

// TestLoadConfig_KeepsPackageName checks that loading leaves package_name,
// strip_prefix and package_prefix as given: the package_name convenience is
// applied once, by the bundler, after every other source has had its say.
// Applied at load time, it could not be overridden: a --package-name, or
// Options.PackageName set after LoadOptions, was ignored.
func TestLoadConfig_KeepsPackageName(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "luamerge.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("package_name: mypkg\nstrip_prefix: custom\n"), 0o600))

	cfg, err := config.LoadConfig(configPath)

	require.NoError(t, err)
	assert.Equal(t, "mypkg", cfg.PackageName)
	assert.Equal(t, "custom", cfg.StripPrefix)
	assert.Empty(t, cfg.PackagePrefix)
}

// TestApplyPackageName checks the package_name convenience: it fills
// strip_prefix and package_prefix unless they are set.
func TestApplyPackageName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                     string
		packageName              string
		stripPrefix, pkgPrefix   string // Before.
		wantStrip, wantPkgPrefix string
	}{
		{"fills both", "mypkg", "", "", "mypkg", "mypkg"},
		{"keeps strip_prefix", "mypkg", "custom", "", "custom", "mypkg"},
		{"keeps package_prefix", "mypkg", "", "custom", "mypkg", "custom"},
		{"no package_name", "", "", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Default()
			cfg.PackageName = tt.packageName
			cfg.StripPrefix = tt.stripPrefix
			cfg.PackagePrefix = tt.pkgPrefix

			cfg.ApplyPackageName()

			assert.Equal(t, tt.wantStrip, cfg.StripPrefix, "strip_prefix")
			assert.Equal(t, tt.wantPkgPrefix, cfg.PackagePrefix, "package_prefix")
		})
	}
}

func TestIncludePackages(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "luamerge.yaml")
	yamlContent := `entry: src/main.lua
include_packages:
  - "plugin.optional"
  - "utils.debug"
`
	require.NoError(t, os.WriteFile(configPath, []byte(yamlContent), 0600))
	cfg, err := config.LoadConfig(configPath)
	require.NoError(t, err)
	assert.Len(t, cfg.IncludePackages, 2, "len(IncludePackages) = %d, want 2", len(cfg.IncludePackages))

	wantFirst, wantSecond := "plugin.optional", "utils.debug"
	assert.Equal(t, wantFirst, cfg.IncludePackages[0],
		"IncludePackages[0] = %q, want %q", cfg.IncludePackages[0], wantFirst)
	assert.Equal(t, wantSecond, cfg.IncludePackages[1],
		"IncludePackages[1] = %q, want %q", cfg.IncludePackages[1], wantSecond)
	// Ensure other fields are default.
	assert.Empty(t, cfg.SkipPackages, "SkipPackages = %v, want empty slice", cfg.SkipPackages)
}

func TestLoadConfigEnvOverrides(t *testing.T) {
	// Env vars must map to the right config keys, including multi-word and
	// nested transform keys. t.Setenv forbids t.Parallel().
	t.Setenv("LUAMERGE_ENTRY", "envmain.lua")
	t.Setenv("LUAMERGE_STRIP_PREFIX", "mylib")
	t.Setenv("LUAMERGE_ARG_FIX", "false")
	t.Setenv("LUAMERGE_TRANSFORM_REMOVE_COMMENTS", "true")
	t.Chdir(t.TempDir()) // No luamerge.yaml.

	cfg, err := config.LoadConfig("")
	require.NoError(t, err)
	assert.Equal(t, "envmain.lua", cfg.Entry)
	assert.Equal(t, "mylib", cfg.StripPrefix)
	assert.False(t, cfg.ArgFix, "LUAMERGE_ARG_FIX=false should disable arg_fix")
	assert.True(t, cfg.Transform.RemoveComments, "LUAMERGE_TRANSFORM_REMOVE_COMMENTS=true should enable it")
}
