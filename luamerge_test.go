package luamerge_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge"
)

// writeProject creates a minimal main + module project and returns its dir.
func writeProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"),
		[]byte("local m = require(\"module\")\nprint(m.greet())\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "module.lua"),
		[]byte("local M = {}\nfunction M.greet() return \"hi\" end\nreturn M\n"), 0o600))

	return dir
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()

	opts := luamerge.DefaultOptions()
	assert.True(t, opts.ArgFix, "arg fix on by default")
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
	assert.Empty(t, res.Warnings)

	out := buf.String()
	assert.Contains(t, out, `package.preload["main"]`)
	assert.Contains(t, out, `package.preload["module"]`)
	assert.Contains(t, out, `return require("main")`)
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
	assert.Contains(t, out, "(loadstring or load)", "debug threaded through")
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

func TestBundleMissingEntry(t *testing.T) {
	t.Parallel()

	_, err := luamerge.Bundle(luamerge.DefaultOptions(), &bytes.Buffer{})
	require.ErrorIs(t, err, luamerge.ErrNoEntry)
	// Sanity: errors.Is works against the exported sentinel.
	assert.ErrorIs(t, err, luamerge.ErrNoEntry)
}

// TestBundleRunsUnderLua proves the public API produces a runnable bundle.
func TestBundleRunsUnderLua(t *testing.T) {
	t.Parallel()

	lua, err := exec.LookPath("lua")
	if err != nil {
		t.Skip("lua interpreter not found in PATH")
	}

	dir := writeProject(t)
	opts := luamerge.DefaultOptions()
	opts.Entry = filepath.Join(dir, "main.lua")
	opts.Root = dir

	var buf bytes.Buffer

	_, err = luamerge.Bundle(opts, &buf)
	require.NoError(t, err)

	bundle := filepath.Join(t.TempDir(), "bundle.lua")
	require.NoError(t, os.WriteFile(bundle, buf.Bytes(), 0o600))

	// #nosec G204 -- lua resolved via exec.LookPath; bundle is a file we just wrote.
	out, err := exec.CommandContext(t.Context(), lua, bundle).CombinedOutput()
	require.NoError(t, err, "bundle should run: %s", out)
	assert.Equal(t, "hi", strings.TrimSpace(string(out)))
}
