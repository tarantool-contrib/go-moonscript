package integration_test

import (
	"testing"

	"github.com/tarantool/go-luamerge/internal/luatest"
)

// TestResolve_IgnoresRequiringFileDirectory checks that a module name means
// the same file wherever it is required from. Lua resolves every name against
// package.path, relative to the working directory: require("util") in
// sub/x.lua loads ./util.lua, never sub/util.lua. The bundle must embed that
// file, for main and for sub.x alike, and --strict must not hide a mismatch.
func TestResolve_IgnoresRequiringFileDirectory(t *testing.T) {
	t.Parallel()

	luatest.Differential(t, differential(luatest.Project{
		"main.lua": `print("main got:  " .. require("util").name)` + "\n" +
			`print("sub.x got: " .. require("sub.x"))` + "\n",
		"util.lua":     `return { name = "util.lua" }` + "\n",
		"sub/util.lua": `return { name = "sub/util.lua" }` + "\n",
		"sub/x.lua":    `return require("util").name` + "\n",
	}, "", luatest.CLI("--entry", "main.lua", "--strict")))
}
