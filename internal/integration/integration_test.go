package integration_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/config"
	"github.com/tarantool/go-luamerge/internal/emit"
	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/parse"
	"github.com/tarantool/go-luamerge/internal/resolve"
)

func findLua() string {
	path, err := exec.LookPath("lua")
	if err == nil {
		return path
	}

	path, err = exec.LookPath("lua5.1")
	if err == nil {
		return path
	}

	path, err = exec.LookPath("lua5.2")
	if err == nil {
		return path
	}

	path, err = exec.LookPath("lua5.3")
	if err == nil {
		return path
	}

	path, err = exec.LookPath("lua5.4")
	if err == nil {
		return path
	}

	return ""
}

func TestIntegration(t *testing.T) {
	t.Parallel()

	luaPath := findLua()
	if luaPath == "" {
		t.Skip("lua interpreter not found in PATH")
	}

	testDirs, err := filepath.Glob("../../testdata/*")
	require.NoError(t, err)

	skipDirs := map[string]bool{
		// Original fails with C stack overflow; amalgamator fixes circular deps.
		"circular": true,
	}

	for _, dir := range testDirs {
		// Skip directories that don't have a main.lua.
		mainPath := filepath.Join(dir, "main.lua")

		_, err := os.Stat(mainPath)
		if err != nil {
			continue
		}

		name := filepath.Base(dir)
		if skipDirs[name] {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runIntegrationTest(t, luaPath, dir, mainPath)
		})
	}
}

func runIntegrationTest(t *testing.T, luaPath, dir, entryPath string) {
	t.Helper()

	// Run original project.
	origOut, origErr := runLua(t, luaPath, entryPath, dir)

	// Build amalgamated bundle.
	cfg := config.Config{
		Entry:    entryPath,
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
	require.NoError(t, cfg.ResolveRoot(), "resolve root")

	parser := parse.New()
	resolver := resolve.New(cfg.Root, nil, cfg.Path)
	g, err := graph.Build(&cfg, parser, resolver)
	require.NoError(t, err, "build graph")

	var buf bytes.Buffer

	emitOpts := emit.Options{
		Prefix:   "",
		Suffix:   "",
		Debug:    false,
		Fallback: false,
		Shebang:  "",
		NoArgFix: false,
	}
	require.NoError(t, emit.Emit(&buf, g, nil, emitOpts), "emit")
	bundle := buf.Bytes()

	// Write bundle to temp file; t.TempDir() handles cleanup.
	tmpFile, err := os.CreateTemp(t.TempDir(), "amalg-test-*.lua")
	require.NoError(t, err, "create temp file")

	_, err = tmpFile.Write(bundle)
	require.NoError(t, err, "write temp file")
	require.NoError(t, tmpFile.Close(), "close temp file")

	// Run bundle.
	bundleOut, bundleErr := runLua(t, luaPath, tmpFile.Name(), dir)

	// Compare outputs.
	assert.Equal(t, origOut, bundleOut, "output mismatch\noriginal output:\n%s\nbundle output:\n%s", origOut, bundleOut)

	// Compare errors: both empty or both non-empty.
	assert.Equal(
		t, origErr == "", bundleErr == "",
		"error presence mismatch\noriginal error:\n%s\nbundle error:\n%s", origErr, bundleErr,
	)
}

// TestDebugTraceback verifies that --debug makes runtime errors report the
// original module file and line number instead of an offset into the bundle.
func TestDebugTraceback(t *testing.T) {
	t.Parallel()

	luaPath := findLua()
	if luaPath == "" {
		t.Skip("lua interpreter not found in PATH")
	}

	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.lua")
	boomPath := filepath.Join(dir, "boom.lua")

	require.NoError(t, os.WriteFile(mainPath, []byte("local m = require(\"boom\")\nm.go()\n"), 0o600))

	// error() is on line 3 of boom.lua.
	boomSrc := "local M = {}\nfunction M.go()\n  error(\"kaboom\")\nend\nreturn M\n"
	require.NoError(t, os.WriteFile(boomPath, []byte(boomSrc), 0o600))

	cfg := config.Config{
		Entry:    mainPath,
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
	require.NoError(t, cfg.ResolveRoot(), "resolve root")
	g, err := graph.Build(&cfg, parse.New(), resolve.New(cfg.Root, nil, cfg.Path))
	require.NoError(t, err, "build graph")

	var buf bytes.Buffer

	emitOpts := emit.Options{
		Prefix:   "",
		Suffix:   "",
		Debug:    true,
		Fallback: false,
		Shebang:  "",
		NoArgFix: false,
	}
	require.NoError(t, emit.Emit(&buf, g, nil, emitOpts), "emit")

	bundlePath := filepath.Join(dir, "bundle.lua")
	require.NoError(t, os.WriteFile(bundlePath, buf.Bytes(), 0o600))

	_, stderr := runLua(t, luaPath, bundlePath, dir)

	// The error origin (first line) must report the original module file:line,
	// not an offset into the bundle.
	errLine, _, found := strings.Cut(stderr, "\n")
	if !found {
		errLine = stderr
	}

	// Lua truncates long chunk names (LUA_IDSIZE) with a leading "...", so match
	// on the base name + line rather than the full temp path.
	want := "boom.lua:3"
	assert.Contains(
		t, errLine, want,
		"debug error should point at original file:line\nwant substring: %q\ngot first line: %q\nfull stderr:\n%s",
		want, errLine, stderr,
	)
	assert.NotContains(
		t, errLine, "bundle.lua:",
		"debug error should not originate from bundle.lua\ngot first line: %q", errLine,
	)
}

// TestCircularDependency verifies that the bundle resolves a require() cycle
// (a -> b -> a). The un-bundled project stack-overflows, so TestIntegration
// skips this dir; here we run the bundle directly and assert it succeeds — this
// is what the `package.loaded[name] = true` guard buys.
func TestCircularDependency(t *testing.T) {
	t.Parallel()

	luaPath := findLua()
	if luaPath == "" {
		t.Skip("lua interpreter not found in PATH")
	}

	dir := "../../testdata/circular"
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
	require.NoError(t, cfg.ResolveRoot())
	g, err := graph.Build(&cfg, parse.New(), resolve.New(cfg.Root, nil, cfg.Path))
	require.NoError(t, err, "build graph")

	var buf bytes.Buffer

	emitOpts := emit.Options{
		Prefix:   "",
		Suffix:   "",
		Debug:    false,
		Fallback: false,
		Shebang:  "",
		NoArgFix: false,
	}
	require.NoError(t, emit.Emit(&buf, g, nil, emitOpts), "emit")

	bundlePath := filepath.Join(t.TempDir(), "bundle.lua")
	require.NoError(t, os.WriteFile(bundlePath, buf.Bytes(), 0o600))

	stdout, stderr := runLua(t, luaPath, bundlePath, dir)
	assert.Empty(t, stderr, "circular bundle should run without error")
	assert.Equal(t, "circular test\na\nb", stdout)
}

// TestFallbackOnDiskOverride verifies that in fallback mode an on-disk module
// found on package.path takes precedence over the embedded copy, while a normal
// bundle uses the embedded copy.
func TestFallbackOnDiskOverride(t *testing.T) {
	t.Parallel()

	luaPath := findLua()
	if luaPath == "" {
		t.Skip("lua interpreter not found in PATH")
	}

	src := t.TempDir()
	mainSrc := "local g = require(\"greet\")\nprint(g())\n"
	require.NoError(t, os.WriteFile(filepath.Join(src, "main.lua"), []byte(mainSrc), 0o600))

	embeddedGreetSrc := "return function() return \"EMBEDDED\" end\n"
	require.NoError(t, os.WriteFile(filepath.Join(src, "greet.lua"), []byte(embeddedGreetSrc), 0o600))

	cfg := config.Config{
		Entry:    filepath.Join(src, "main.lua"),
		Output:   "",
		Root:     src,
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
	require.NoError(t, cfg.ResolveRoot())
	g, err := graph.Build(&cfg, parse.New(), resolve.New(cfg.Root, nil, cfg.Path))
	require.NoError(t, err, "build graph")

	emitBundle := func(opts emit.Options) string {
		var buf bytes.Buffer
		require.NoError(t, emit.Emit(&buf, g, nil, opts), "emit")

		return buf.String()
	}

	// Run directory holds an on-disk override of greet reachable via ./?.lua.
	run := t.TempDir()
	onDiskGreetSrc := "return function() return \"ON-DISK\" end\n"
	require.NoError(t, os.WriteFile(filepath.Join(run, "greet.lua"), []byte(onDiskGreetSrc), 0o600))

	normalOpts := emit.Options{
		Prefix: "", Suffix: "", Debug: false, Fallback: false, Shebang: "", NoArgFix: false,
	}
	require.NoError(t, os.WriteFile(filepath.Join(run, "normal.lua"), []byte(emitBundle(normalOpts)), 0o600))

	fallbackOpts := emit.Options{
		Prefix: "", Suffix: "", Debug: false, Fallback: true, Shebang: "", NoArgFix: false,
	}
	require.NoError(t, os.WriteFile(filepath.Join(run, "fallback.lua"), []byte(emitBundle(fallbackOpts)), 0o600))

	runWithPath := func(script string) string {
		// #nosec G204 -- script is one of the fixed bundle files this test just wrote to run, not attacker input.
		cmd := exec.CommandContext(t.Context(), luaPath, script)
		cmd.Dir = run

		cmd.Env = append(os.Environ(), "LUA_PATH=./?.lua;;")

		var stdout, stderr bytes.Buffer

		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		require.NoError(t, cmd.Run(), "run %s: %s", script, stderr.String())

		return strings.TrimSpace(stdout.String())
	}

	assert.Equal(t, "EMBEDDED", runWithPath("normal.lua"), "normal bundle should use the embedded module")
	assert.Equal(t, "ON-DISK", runWithPath("fallback.lua"), "fallback bundle should prefer the on-disk module")
}

func runLua(t *testing.T, luaPath, scriptPath, workingDir string) (string, string) {
	t.Helper()

	// #nosec G204 -- luaPath is resolved via exec.LookPath in findLua and scriptPath is a bundle this test just wrote.
	cmd := exec.CommandContext(t.Context(), luaPath, scriptPath)
	cmd.Dir = workingDir

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run error is ignored on purpose; the caller inspects stdout/stderr instead.
	_ = cmd.Run()

	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String())
}
