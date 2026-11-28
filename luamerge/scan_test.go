package luamerge_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge"
)

// TestBundle_TokenScanWarning: a module the parser cannot read is bundled
// with its requires, found by the token scanner, and the API says so with a
// warning of its own kind that names the file and the line the parser failed
// on.
func TestBundle_TokenScanWarning(t *testing.T) {
	t.Parallel()

	// Warnings name files by their paths with symbolic links resolved.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	for name, content := range map[string]string{
		"main.lua":   "local ok = pcall(require, \"broken\")\nprint(ok)\n",
		"broken.lua": "local x = = 1\nreturn require(\"module\")\n",
		"module.lua": "return {}\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}

	res, out := testStrictBundle(t, dir)
	require.Len(t, res.Warnings, 1, "a module the parser cannot read is a warning, even in strict mode")

	w := res.Warnings[0]
	assert.Equal(t, luamerge.WarnTokenScan, w.Kind)
	assert.Equal(t, "token-scan", w.Kind.String())
	assert.Equal(t, filepath.Join(dir, "broken.lua"), w.File)
	assert.Equal(t, 1, w.Line, "the line the parser failed on")
	assert.Contains(t, w.Message, `unexpected symbol near "="`)
	assert.Contains(t, out, `package.preload["broken"]`)
	assert.Contains(t, out, `package.preload["module"]`, "the require the token scanner found is embedded")
}
