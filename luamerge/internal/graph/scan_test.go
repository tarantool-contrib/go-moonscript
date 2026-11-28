package graph_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/graph"
)

// TestBuild_TokenScanWarning: a module the parser cannot read does not fail
// the build. The token scanner finds its requires, which are embedded, and a
// warning of its own kind says where and why the parser failed, even in
// strict mode.
func TestBuild_TokenScanWarning(t *testing.T) {
	t.Parallel()

	g, dir := testBuild(t, map[string]string{
		"main.lua": "local m = require(\"m\")\n",
		"m.lua":    "local x = = 1\nreturn require(\"n\")\n",
		"n.lua":    "return {}\n",
	})

	assert.ElementsMatch(t, []string{"m", "n"}, testModuleNames(g), "embedded modules")
	assert.Equal(t, []graph.Warning{
		{
			Kind:   graph.WarnTokenScan,
			Module: "",
			File:   filepath.Join(dir, "m.lua"),
			Line:   1,
			Message: `the parser cannot read the file: unexpected symbol near "="; ` +
				"its requires were found by the token scanner, which takes a local named require for the global one",
		},
	}, g.Warnings)
	assert.Equal(t, "token-scan", graph.WarnTokenScan.String())
}
