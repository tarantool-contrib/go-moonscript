package integration_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestResolve_PathStyleNames checks names written as paths. Lua's
// package.searchpath replaces every "." of a name with "/" and resolves the
// result against package.path, so require("./foo"), require("../foo") and
// require(".foo") all load ./foo.lua, and require("a/b.c") loads
// ./a/b/c.lua. The project is app/, with a foo.lua next to it: a "../" in a
// name must not reach outside the project.
func TestResolve_PathStyleNames(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{
		"foo.lua": `return "../foo.lua (outside the project)"` + "\n",
		"app/main.lua": `require("sub.x")` + "\n" +
			`print("../foo ->", (require("../foo")))` + "\n" +
			`print(".foo   ->", (require(".foo")))` + "\n" +
			`print("a/b.c  ->", (require("a/b.c")))` + "\n",
		"app/sub/x.lua":   `print("./foo   ->", (require("./foo")))` + "\n",
		"app/foo.lua":     `return "./foo.lua"` + "\n",
		"app/.foo.lua":    `return "./.foo.lua"` + "\n",
		"app/sub/foo.lua": `return "sub/foo.lua"` + "\n",
		"app/a/b/c.lua":   `return "./a/b/c.lua"` + "\n",
	})
	app := filepath.Join(dir, "app")

	bundle := luatest.CLI("--entry", "main.lua")(t, app)
	assert.NotContains(t, string(bundle), "outside the project",
		"the bundle embeds ../foo.lua, a file outside the project root")

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		opts := luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}
		luatest.AssertSame(t, luatest.Run(t, interp, app, opts, "main.lua"), luatest.RunBundle(t, interp, bundle, opts))
	})
}

// TestResolve_SearchDirectoryNameThroughALink checks the name of a search
// directory reached through a symbolic link, where the name is stripped as a
// prefix: it is the name of the directory the link leads to, as the root's
// is. With --search vlink, a link to vendor, "vendor.x" is x in vendor and
// "vlink.x" is not stripped.
func TestResolve_SearchDirectoryNameThroughALink(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{
		"src/main.lua": "require(\"vendor.x\")\nrequire(\"vlink.x\")\n",
		"vendor/x.lua": "return 1\n",
	})
	require.NoError(t, os.Symlink("vendor", filepath.Join(dir, "vlink")))

	r := luatest.RunCLI(t, dir, nil, "--entry", "src/main.lua", "--search", "vlink")
	require.Equal(t, 0, r.ExitCode, "%s", r.Stderr)

	assert.Contains(t, r.Stdout, `package.preload["vendor.x"]`, "vendor.x is x in the search directory")
	assert.NotContains(t, r.Stdout, `package.preload["vlink.x"]`, "vlink.x is not stripped")
	assert.Contains(t, r.Stderr, `unresolved require "vlink.x"`)
}
