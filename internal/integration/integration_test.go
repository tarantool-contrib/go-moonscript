package integration_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/config"
	"github.com/tarantool/go-luamerge/internal/emit"
	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/luatest"
	"github.com/tarantool/go-luamerge/internal/parse"
	"github.com/tarantool/go-luamerge/internal/resolve"
)

// TestMain lets tests here bundle through the CLI (luatest.CLI): luatest.Main
// removes the binary it builds.
func TestMain(m *testing.M) { luatest.Main(m) }

// bundler builds the project in dir the way these tests always have: the
// default path templates, the project directory as root, graph.Build and
// emit.Emit without transforms. tweak, when set, adjusts the config first.
func bundler(opts emit.Options, tweak func(cfg *config.Config)) luatest.Bundler {
	return func(tb testing.TB, dir string) []byte {
		tb.Helper()

		cfg := config.Config{
			Entry:    filepath.Join(dir, "main.lua"),
			Output:   "",
			Root:     dir,
			Path:     "?.lua;?/init.lua",
			Search:   nil,
			Strict:   false,
			Debug:    false,
			Fallback: false,
			ArgFix:   false,
			Transform: config.TransformConfig{
				RemoveComments:   false,
				RemoveEmptyLines: false,
				Minify:           false,
				StripShebang:     false,
			},
			Prefix:          "",
			Suffix:          "",
			Shebang:         "",
			PackagePrefix:   "",
			PackageName:     "",
			StripPrefix:     "",
			SkipPackages:    nil,
			IncludePackages: nil,
		}
		if tweak != nil {
			tweak(&cfg)
		}

		require.NoError(tb, cfg.ResolveRoot(), "resolve root")

		g, err := graph.Build(&cfg, parse.New(), resolve.New(cfg.Root, nil, cfg.Path))
		require.NoError(tb, err, "build graph")

		var buf bytes.Buffer

		require.NoError(tb, emit.Emit(&buf, g, nil, opts), "emit")

		return buf.Bytes()
	}
}

// emitOptions are the emit options of these tests: everything off except
// what debug and fallback say.
func emitOptions(debug, fallback bool) emit.Options {
	return emit.Options{Prefix: "", Suffix: "", Debug: debug, Fallback: fallback, Shebang: "", NoArgFix: false}
}

// differential is a luatest.Case with the given project and bundler and
// every other field at its default.
func differential(project luatest.Project, dir string, bundle luatest.Bundler) luatest.Case {
	return luatest.Case{
		Project: project,
		Dir:     dir,
		Entry:   "",
		Bundle:  bundle,
		Args:    nil,
		Options: luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0},
		Check:   nil,
	}
}

// TestIntegration bundles every testdata project that has a main.lua and
// checks, on every interpreter, that the bundle run from an empty directory
// behaves like the original run from the project directory.
func TestIntegration(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t) // Skip or fail once, not per fixture.

	testDirs, err := filepath.Glob("../../testdata/*")
	require.NoError(t, err)

	checks := map[string]func(t *testing.T, orig, bundle luatest.Result){
		// The fixture's requires form a cycle, which fails natively.
		"circular": assertFailsLikeACycle,
	}
	tweaks := map[string]func(cfg *config.Config){
		// require(modname) cannot be resolved statically: the bundler warns and
		// leaves it to runtime. --include is how its target gets embedded; the
		// fixture used to pass only because the bundle ran next to module.lua.
		"dynamic": func(cfg *config.Config) { cfg.IncludePackages = []string{"module"} },
	}

	projects := 0

	for _, dir := range testDirs {
		_, err := os.Stat(filepath.Join(dir, "main.lua"))
		if err != nil {
			continue
		}

		projects++
		name := filepath.Base(dir)

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			spec := differential(nil, dir, bundler(emitOptions(false, false), tweaks[name]))
			spec.Check = checks[name]

			luatest.Differential(t, spec)
		})
	}

	require.NotZero(t, projects, "no testdata project with a main.lua under ../../testdata")
}

// TestDebugTraceback verifies that --debug makes runtime errors report the
// original module file and line number instead of an offset into the bundle.
func TestDebugTraceback(t *testing.T) {
	t.Parallel()

	spec := differential(luatest.Project{
		"main.lua": "local m = require(\"boom\")\nm.go()\n",
		// error() is on line 3 of boom.lua.
		"boom.lua": "local M = {}\nfunction M.go()\n  error(\"kaboom\")\nend\nreturn M\n",
	}, "", bundler(emitOptions(true, false), nil))
	spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
		t.Helper()

		luatest.AssertSame(t, orig, bundle)

		// The error origin (first line) must report the original module
		// file:line, not an offset into the bundle.
		errLine, _, _ := strings.Cut(bundle.Stderr, "\n")

		// Lua truncates long chunk names (LUA_IDSIZE) with a leading "...", so
		// match on the base name + line rather than the full temp path.
		want := "boom.lua:3"
		assert.Contains(
			t, errLine, want,
			"debug error should point at original file:line\nwant substring: %q\ngot first line: %q\nfull stderr:\n%s",
			want, errLine, bundle.Stderr,
		)
		assert.NotContains(
			t, errLine, "bundle.lua:",
			"debug error should not originate from bundle.lua\ngot first line: %q", errLine,
		)
	}

	luatest.Differential(t, spec)
}

// TestCircularDependency verifies that a require() cycle (a -> b -> a) fails
// in the bundle the way it fails in the original program, in both modes:
// require itself manages package.loaded, so nothing hands the module that is
// still loading a placeholder value.
func TestCircularDependency(t *testing.T) {
	t.Parallel()

	for name, debug := range map[string]bool{"default": false, "debug": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			spec := differential(nil, "../../testdata/circular", bundler(emitOptions(debug, false), nil))
			spec.Check = assertFailsLikeACycle

			luatest.Differential(t, spec)
		})
	}
}

// TestFallbackOnDiskOverride verifies that in fallback mode an on-disk module
// found on package.path takes precedence over the embedded copy, while a normal
// bundle uses the embedded copy.
func TestFallbackOnDiskOverride(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	src := luatest.WriteProject(t, luatest.Project{
		"main.lua":  "local g = require(\"greet\")\nprint(g())\n",
		"greet.lua": "return function() return \"EMBEDDED\" end\n",
	})
	normal := bundler(emitOptions(false, false), nil)(t, src)
	fallback := bundler(emitOptions(false, true), nil)(t, src)

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		// The run directory holds an on-disk override of greet, reachable via
		// ./?.lua on the default LUA_PATH.
		run := luatest.WriteProject(t, luatest.Project{
			"greet.lua":    "return function() return \"ON-DISK\" end\n",
			"normal.lua":   string(normal),
			"fallback.lua": string(fallback),
		})
		runScript := func(script string) string {
			r := luatest.Run(t, interp, run, luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}, script)
			require.Equal(t, 0, r.ExitCode, "run %s\n%s", script, r)

			return r.Stdout
		}

		assert.Equal(t, "EMBEDDED\n", runScript("normal.lua"), "normal bundle should use the embedded module")
		assert.Equal(t, "ON-DISK\n", runScript("fallback.lua"), "fallback bundle should prefer the on-disk module")
	})
}
