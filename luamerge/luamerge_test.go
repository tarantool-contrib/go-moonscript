package luamerge_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge"
	"github.com/tarantool/go-luamerge/internal/luatest"
)

// writeProject creates a minimal main + module project and returns its dir,
// with its symbolic links resolved as Bundle resolves them in the paths it
// reports (on macOS the temporary directory is under /var, a link to
// /private/var).
func writeProject(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"),
		[]byte("local m = require(\"module\")\nprint(m.greet())\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "module.lua"),
		[]byte("local M = {}\nfunction M.greet() return \"hi\" end\nreturn M\n"), 0o600))

	return dir
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()

	opts := luamerge.DefaultOptions()
	assert.False(t, opts.NoArgFix, "the arg line is on by default")
	assert.Equal(t, "?.lua;?/init.lua", opts.Path)
	assert.False(t, opts.Debug)
}

func TestBundle(t *testing.T) {
	t.Parallel()

	dir := writeProject(t)
	opts := luamerge.DefaultOptions()
	opts.Entry = filepath.Join(dir, "main.lua")
	opts.Root = dir

	var buf bytes.Buffer

	res, err := luamerge.Bundle(opts, &buf)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Nil(t, res.Warnings, "no warnings: the zero value")

	out := buf.String()
	assert.Contains(t, out, `package.preload["module"]`)
	assert.NotContains(t, out, `package.preload["main"]`, "the entry runs as the main chunk, not as a module")
	assert.True(t, strings.HasSuffix(out, "\nprint(m.greet())\n"), "the bundle ends with the entry's code\n%s", out)
}

// TestBundle_ZeroValueOptions checks that Options with nothing but Entry set
// bundle exactly like DefaultOptions with that entry, the defaults of the CLI:
// only Entry must be set. The zero value used to leave out the arg line,
// which is on by default.
func TestBundle_ZeroValueOptions(t *testing.T) {
	t.Parallel()

	// Arrange: a module found through each template of the default path.
	dir := writeProject(t)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "pkg"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "init.lua"), []byte("return \"pkg\"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"),
		[]byte("local m = require(\"module\")\nprint(m.greet(), require(\"pkg\"))\n"), 0o600))

	entry := filepath.Join(dir, "main.lua")
	defaults := luamerge.DefaultOptions()
	defaults.Entry = entry

	var zero luamerge.Options

	zero.Entry = entry

	// Act.
	var want, got bytes.Buffer

	_, errWant := luamerge.Bundle(defaults, &want)
	_, errGot := luamerge.Bundle(zero, &got)

	// Assert.
	require.NoError(t, errWant)
	require.NoError(t, errGot)
	assert.Contains(t, want.String(), `package.preload["pkg"]`, "the fixture reaches pkg/init.lua")
	assert.Equal(t, want.String(), got.String())
}

// TestBundle_Files checks that Result.Files lists every embedded source file
// as an absolute path, the entry first, and nothing that was left to runtime.
// The paths have the root's symbolic links resolved (on macOS the temporary
// directory is under /var, a link to /private/var).
//
//nolint:paralleltest // t.Chdir changes the working directory of the whole process.
func TestBundle_Files(t *testing.T) {
	// Arrange.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "lib"), 0o750))

	for name, src := range map[string]string{
		"main.lua":      "require(\"lib.a\")\nrequire(\"b\")\nrequire(\"nope\")\nrequire(\"skipped\")\n",
		"lib/a.lua":     "return require(\"b\")\n",
		"b.lua":         "return 1\n",
		"skipped.lua":   "return 2\n",
		"unrelated.lua": "return 3\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600))
	}

	// A relative entry still yields absolute paths.
	t.Chdir(dir)

	opts := luamerge.DefaultOptions()
	opts.Entry = "main.lua"
	opts.SkipPackages = []string{"skipped"}

	// Act.
	res, err := luamerge.Bundle(opts, &bytes.Buffer{})

	// Assert.
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(dir, "main.lua"),
		filepath.Join(dir, "lib", "a.lua"),
		filepath.Join(dir, "b.lua"),
	}, res.Files)
}

func TestBundleOptionsThread(t *testing.T) {
	t.Parallel()

	dir := writeProject(t)
	opts := luamerge.DefaultOptions()
	opts.Entry = filepath.Join(dir, "main.lua")
	opts.Root = dir
	opts.Debug = true
	opts.Shebang = "#!/usr/bin/env lua"

	var buf bytes.Buffer

	_, err := luamerge.Bundle(opts, &buf)
	require.NoError(t, err)

	out := buf.String()
	assert.True(t, strings.HasPrefix(out, "#!/usr/bin/env lua\n"), "shebang threaded through")
	assert.Contains(t, out, `(rawget(_G, "loadstring") or load)`, "debug threaded through")
}

func TestBundleWarnings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// A dynamic require can't be resolved statically -> warning, not error.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"),
		[]byte("local n = \"mod\"\nrequire(n)\n"), 0o600))

	opts := luamerge.DefaultOptions()
	opts.Entry = filepath.Join(dir, "main.lua")
	opts.Root = dir

	var buf bytes.Buffer

	res, err := luamerge.Bundle(opts, &buf)
	require.NoError(t, err)
	require.Len(t, res.Warnings, 1, "dynamic require should produce a warning")
	// Warnings are machine-readable, not just strings.
	assert.Equal(t, luamerge.WarnDynamicRequire, res.Warnings[0].Kind)
}

func TestBundleUnresolvedWarningKind(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"),
		[]byte("require(\"nope\")\n"), 0o600))

	opts := luamerge.DefaultOptions()
	opts.Entry = filepath.Join(dir, "main.lua")
	opts.Root = dir

	res, err := luamerge.Bundle(opts, &bytes.Buffer{})
	require.NoError(t, err)
	require.Len(t, res.Warnings, 1)
	assert.Equal(t, luamerge.WarnUnresolved, res.Warnings[0].Kind)
	assert.Equal(t, "nope", res.Warnings[0].Module)
}

// TestBundle_WarningLines: warnings and errors name the line of the require
// in the file, whatever the file starts with. A require on line 3 after two
// blank lines used to be reported at line 1.
func TestBundle_WarningLines(t *testing.T) {
	t.Parallel()

	for name, header := range map[string]string{
		"blank lines":     "\n\n",
		"shebang":         "#!/usr/bin/env lua\n\n",
		"byte order mark": "\xEF\xBB\xBF\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Arrange: a dynamic require on line 3, a missing module on line 4.
			// The directory's symbolic links are resolved, as Bundle resolves
			// them in the paths it reports.
			dir, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)

			entry := filepath.Join(dir, "main.lua")
			require.NoError(t, os.WriteFile(entry,
				[]byte(header+"local m = require(os.getenv(\"X\"))\nrequire(\"nope\")\n"), 0o600))

			opts := luamerge.DefaultOptions()
			opts.Entry = entry

			// Act.
			res, err := luamerge.Bundle(opts, &bytes.Buffer{})

			opts.Strict = true
			_, strictErr := luamerge.Bundle(opts, &bytes.Buffer{})

			// Assert.
			require.NoError(t, err)
			require.Len(t, res.Warnings, 2)
			assert.Equal(t, luamerge.Warning{
				Kind:    luamerge.WarnDynamicRequire,
				Module:  "",
				File:    entry,
				Line:    3,
				Message: "dynamic require, cannot resolve",
			}, res.Warnings[0])
			assert.Equal(t, luamerge.WarnUnresolved, res.Warnings[1].Kind)
			assert.Equal(t, 4, res.Warnings[1].Line, "the line of the unresolved require")
			assert.True(t, strings.HasPrefix(res.Warnings[1].Message, `unresolved require "nope": `),
				"the message: %s", res.Warnings[1].Message)

			var ue *luamerge.UnresolvedError
			require.ErrorAs(t, strictErr, &ue)
			assert.Equal(t, 4, ue.Line, "the line of the unresolved require in strict mode")
		})
	}
}

func TestBundleStrictError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"),
		[]byte("require(\"does_not_exist\")\n"), 0o600))

	opts := luamerge.DefaultOptions()
	opts.Entry = filepath.Join(dir, "main.lua")
	opts.Root = dir
	opts.Strict = true

	var buf bytes.Buffer

	_, err := luamerge.Bundle(opts, &buf)
	require.Error(t, err, "strict mode should fail on an unresolved require")

	// The error is classifiable both as a typed error and via its cause.
	var ue *luamerge.UnresolvedError
	require.ErrorAs(t, err, &ue)
	assert.Equal(t, "does_not_exist", ue.Module)
	assert.ErrorIs(t, err, luamerge.ErrModuleNotFound)
}

// TestBundle_UnresolvedError checks the error Bundle returns in strict mode
// for a module that cannot be found: its fields, its message, which gives the
// position of the require when there is one, and its cause.
func TestBundle_UnresolvedError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		main    string
		include []string
		module  string
		line    int
		inFile  bool
	}{
		{name: "require", main: "local x\nrequire(\"nope\")\n", include: nil, module: "nope", line: 2, inFile: true},
		{name: "include", main: "return 1\n", include: []string{"none"}, module: "none", line: 0, inFile: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			dir := testResolvedDir(t)
			entry := filepath.Join(dir, "main.lua")
			require.NoError(t, os.WriteFile(entry, []byte(tt.main), 0o600))

			opts := luamerge.DefaultOptions()
			opts.Entry = entry
			opts.IncludePackages = tt.include
			opts.Strict = true

			file, position := "", ""
			if tt.inFile {
				file = entry
				position = fmt.Sprintf(" at %s:%d", entry, tt.line)
			}

			// Act.
			_, err := luamerge.Bundle(opts, &bytes.Buffer{})

			// Assert.
			var unresolved *luamerge.UnresolvedError
			require.ErrorAs(t, err, &unresolved)
			assert.Equal(t, tt.module, unresolved.Module)
			assert.Equal(t, file, unresolved.File)
			assert.Equal(t, tt.line, unresolved.Line)
			require.ErrorIs(t, unresolved.Err, luamerge.ErrModuleNotFound, "the cause")
			assert.Equal(t,
				fmt.Sprintf("luamerge: build graph: unresolved require %q%s: %v", tt.module, position, unresolved.Err),
				err.Error(), "the message")
		})
	}
}

func TestBundleMissingEntry(t *testing.T) {
	t.Parallel()

	_, err := luamerge.Bundle(luamerge.DefaultOptions(), &bytes.Buffer{})
	require.ErrorIs(t, err, luamerge.ErrNoEntry)
	// Sanity: errors.Is works against the exported sentinel.
	assert.ErrorIs(t, err, luamerge.ErrNoEntry)
}

// TestBundleRunsUnderLua proves the public API produces a runnable bundle: run
// from an empty directory on every interpreter, it behaves like the project.
func TestBundleRunsUnderLua(t *testing.T) {
	t.Parallel()

	luatest.Differential(t, luatest.Case{
		Project: nil,
		Dir:     writeProject(t),
		Entry:   "",
		Bundle: func(tb testing.TB, dir string) []byte {
			tb.Helper()

			opts := luamerge.DefaultOptions()
			opts.Entry = filepath.Join(dir, "main.lua")
			opts.Root = dir

			var buf bytes.Buffer

			_, err := luamerge.Bundle(opts, &buf)
			require.NoError(tb, err)

			return buf.Bytes()
		},
		Args:    nil,
		Options: luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0},
		Check: func(t *testing.T, orig, bundle luatest.Result) {
			t.Helper()

			luatest.AssertSame(t, orig, bundle)
			assert.Equal(t, "hi\n", bundle.Stdout)
		},
	})
}
