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
