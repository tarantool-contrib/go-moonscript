package integration_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/luatest"
)

// The tests in this file are about --package-prefix and --package-name, which
// bundle a library for other programs to require.

// TestCLI_PackagePrefixNameTaken: with --package-prefix p, x.lua required as
// "x" would be registered as "p.x", which is the name of p/x.lua. The bundle
// registers one module under a name, so one file would take the place of the
// other. The CLI refuses to bundle it, naming both files.
func TestCLI_PackagePrefixNameTaken(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{
		"main.lua": "print((require(\"x\")), (require(\"p.x\")))\n",
		"x.lua":    "return \"X\"\n",
		"p/x.lua":  "return \"PX\"\n",
	})
	root, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	r := luatest.RunCLI(t, dir, nil, "--entry", "main.lua", "--package-prefix", "p", "--output", "-")

	assert.Equal(t, 1, r.ExitCode, "the build fails\nstderr:\n%s", r.Stderr)
	assert.Empty(t, r.Stdout, "no bundle is written")
	assert.Contains(t, r.Stderr, filepath.Join(root, "x.lua"), "the message names the module that gets the name")
	assert.Contains(t, r.Stderr, filepath.Join(root, "p", "x.lua"), "and the module that has it")
	assert.Contains(t, r.Stderr, "choose another prefix")
}
