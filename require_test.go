package luamerge_test

import (
	"bytes"
	"fmt"
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

// testResolvedDir is a fresh temporary directory with its symbolic links
// resolved, as Bundle resolves them in the paths it reports (on macOS the
// temporary directory is under /var, a link to /private/var).
func testResolvedDir(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	return dir
}

// testStandardLibrary are the modules every Lua interpreter has in
// package.loaded before the program starts.
func testStandardLibrary() []string {
	return []string{"_G", "coroutine", "debug", "io", "math", "os", "package", "string", "table"}
}

// TestBundle_StandardLibraryModules: require returns a standard library
// module from package.loaded without searching, so a require of one is
// neither resolved nor embedded nor reported, even in strict mode, and even
// when the project has a file of that name, which natively never loads.
func TestBundle_StandardLibraryModules(t *testing.T) {
	t.Parallel()

	for _, name := range testStandardLibrary() {
		for variant, withFile := range map[string]bool{"no file": false, "project file": true} {
			t.Run(name+"/"+variant, func(t *testing.T) {
				t.Parallel()

				dir := testResolvedDir(t)
				main := filepath.Join(dir, "main.lua")
				require.NoError(t, os.WriteFile(main,
					fmt.Appendf(nil, "local m = require(%q)\nprint(type(m))\n", name), 0o600))

				if withFile {
					require.NoError(t, os.WriteFile(filepath.Join(dir, name+".lua"),
						[]byte("error(\"never loaded natively\")\n"), 0o600))
				}

				res, out := testStrictBundle(t, dir)
				assert.Empty(t, res.Warnings)
				assert.Equal(t, []string{main}, res.Files, "nothing but the entry is embedded")
				assert.NotContains(t, out, fmt.Sprintf("[%q]", name), "no loader is registered for it")
			})
		}
	}
}

// TestBundle_RuntimeBuiltinWithoutAFile: a module that some runtime has built
// in, and the project has no file for, is the runtime's to provide, so it is
// no unresolved require: no warning, and no error in strict mode. One name of
// each runtime that has any.
func TestBundle_RuntimeBuiltinWithoutAFile(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"bit32",         // Lua 5.2 and 5.3.
		"utf8",          // Lua 5.3 and later, Tarantool.
		"bit",           // LuaJIT, Tarantool.
		"table.new",     // LuaJIT, Tarantool.
		"string.buffer", // LuaJIT.
		"thread.exdata", // OpenResty's LuaJIT.
		"csv",           // Tarantool.
		"zlib",          // Tarantool's static build.
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := testResolvedDir(t)
			main := filepath.Join(dir, "main.lua")
			require.NoError(t, os.WriteFile(main,
				fmt.Appendf(nil, "local m = require(%q)\nprint(type(m))\n", name), 0o600))

			res, out := testStrictBundle(t, dir)
			assert.Empty(t, res.Warnings)
			assert.Equal(t, []string{main}, res.Files, "nothing but the entry is embedded")
			assert.NotContains(t, out, fmt.Sprintf("[%q]", name), "no loader is registered for it")
		})
	}
}

// TestBundle_ProtectedRequireIsOptional: a module required in protected mode,
// `pcall(require, "cjson")`, is an optional dependency: the program is
// written to run on without it. When the module is not found, that is a
// warning even in strict mode, and the require is left to runtime.
func TestBundle_ProtectedRequireIsOptional(t *testing.T) {
	t.Parallel()

	for name, call := range map[string]string{
		"pcall":  `pcall(require, "cjson")`,
		"xpcall": `xpcall(require, debug.traceback, "cjson")`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := testResolvedDir(t)
			main := filepath.Join(dir, "main.lua")
			require.NoError(t, os.WriteFile(main,
				[]byte("local ok = "+call+"\nprint(ok and \"cjson\" or \"fallback\")\n"), 0o600))

			res, out := testStrictBundle(t, dir)
			require.Len(t, res.Warnings, 1)

			w := res.Warnings[0]
			assert.Equal(t, luamerge.WarnUnresolved, w.Kind)
			assert.Equal(t, "cjson", w.Module)
			assert.Equal(t, main, w.File)
			assert.Equal(t, 1, w.Line)
			assert.Contains(t, w.Message, "protected", "the warning says why strict mode lets it pass")
			assert.NotContains(t, out, `["cjson"]`)
		})
	}
}

// TestBundle_UnprotectedRequireStaysStrict: a module the program also
// requires without protection is not optional, so strict mode still fails
// on it.
func TestBundle_UnprotectedRequireStaysStrict(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"),
		[]byte("local ok = pcall(require, \"cjson\")\nlocal cjson = require(\"cjson\")\n"), 0o600))

	opts := luamerge.DefaultOptions()
	opts.Entry = filepath.Join(dir, "main.lua")
	opts.Root = dir
	opts.Strict = true

	_, err := luamerge.Bundle(opts, &bytes.Buffer{})

	var ue *luamerge.UnresolvedError
	require.ErrorAs(t, err, &ue)
	assert.Equal(t, "cjson", ue.Module)
	assert.Equal(t, 2, ue.Line, "the unprotected require")
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
