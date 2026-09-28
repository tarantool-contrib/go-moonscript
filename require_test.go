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

// testStrictBundle bundles the project in dir from main.lua in strict mode and
// returns the result and the bundle.
func testStrictBundle(t *testing.T, dir string) (*luamerge.Result, string) {
	t.Helper()

	opts := luamerge.DefaultOptions()
	opts.Entry = filepath.Join(dir, "main.lua")
	opts.Root = dir
	opts.Strict = true

	var buf bytes.Buffer

	res, err := luamerge.Bundle(opts, &buf)
	require.NoError(t, err)

	return res, buf.String()
}

// TestBundle_RequireAsMethodReceiver: a module whose require is the receiver of
// a method call is embedded, and nothing is reported.
func TestBundle_RequireAsMethodReceiver(t *testing.T) {
	t.Parallel()

	dir := writeProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"),
		[]byte("local c = require(\"counter\"):new(5)\nprint(\"counter:\", c.n)\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "counter.lua"),
		[]byte("local C = {}\nC.__index = C\n"+
			"function C:new(n) return setmetatable({ n = n }, self) end\nreturn C\n"), 0o600))

	res, out := testStrictBundle(t, dir)
	assert.Empty(t, res.Warnings)
	assert.Contains(t, out, `package.preload["counter"]`)
}

// TestBundle_IndirectRequireWarning: an aliased require hides the modules
// loaded through it, and the API says so with a warning of its own kind rather
// than dropping them silently.
func TestBundle_IndirectRequireWarning(t *testing.T) {
	t.Parallel()

	dir := writeProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"),
		[]byte("local r = require\nlocal m = r(\"module\")\nprint(m.greet())\n"), 0o600))

	res, out := testStrictBundle(t, dir)
	require.Len(t, res.Warnings, 1, "an indirect require is a warning, not an error, even in strict mode")

	w := res.Warnings[0]
	assert.Equal(t, luamerge.WarnIndirectRequire, w.Kind)
	assert.Equal(t, "indirect-require", w.Kind.String())
	assert.Equal(t, filepath.Join(dir, "main.lua"), w.File)
	assert.Equal(t, 1, w.Line)
	assert.Empty(t, w.Module, "no module name is known")
	assert.Contains(t, w.Message, `aliased as local "r"`, "the warning names the alias")
	assert.NotContains(t, out, `package.preload["module"]`,
		"a module loaded only through the alias cannot be embedded")
}

// TestBundle_GlobalTableAndXpcallRequire: `_G.require("x")`,
// `_ENV.require("x")` and `xpcall(require, handler, "x")` name their module
// literally, so it is embedded.
func TestBundle_GlobalTableAndXpcallRequire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		main string
	}{
		{
			name: "_G.require",
			main: "local m = _G.require(\"module\")\nprint(m.greet())\n",
		},
		{
			name: "_ENV.require",
			main: "local m = _ENV.require(\"module\")\nprint(m.greet())\n",
		},
		{
			name: "xpcall",
			main: "local ok, m = xpcall(require, debug.traceback, \"module\")\nprint(ok and m.greet())\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeProject(t)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"), []byte(tt.main), 0o600))

			res, out := testStrictBundle(t, dir)
			assert.Empty(t, res.Warnings)
			assert.Contains(t, out, `package.preload["module"]`)
		})
	}
}
