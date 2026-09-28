//go:build unix

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/luatest"
)

// Guards for the way the CLI puts the bundle on disk that need POSIX file
// modes, device files and symlinks, so they are Unix-only.

// withUmask sets the process umask for the rest of the test; the CLI child
// inherits it. The umask belongs to the whole process, so a test using this
// must not run in parallel: parallel tests resume only once every sequential
// test has finished.
func withUmask(t *testing.T, mask int) {
	t.Helper()

	old := syscall.Umask(mask)

	t.Cleanup(func() { syscall.Umask(old) })
}

// buildTo bundles the project in dir into out and requires success.
func buildTo(t *testing.T, dir, out string) {
	t.Helper()

	_, stderr, code := runCLI(t, append(entryArgs(dir), "--output", out)...)
	require.Equal(t, 0, code, "build into %s: %s", out, stderr)
}

// perm returns the permission bits of path in octal, the form a failure
// should read in.
func perm(t *testing.T, path string) string {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err)

	return fmt.Sprintf("%04o", info.Mode().Perm())
}

// TestCLI_OutputMode checks that a new output gets the mode os.Create would
// give it (0666 less the umask) and that a rebuilt output keeps its permission
// bits: the README's `--output app` followed by `chmod +x app` must stay
// executable after the next build. 0775 is a mode the umask would strip, and
// 0600 one it would loosen, were the bits taken from anywhere but the old file.
//
//nolint:paralleltest // withUmask changes the umask of the whole process.
func TestCLI_OutputMode(t *testing.T) {
	withUmask(t, 0o022)

	dir := writeProject(t)

	t.Run("new file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "bundle.lua")

		buildTo(t, dir, out)

		assert.Equal(t, "0644", perm(t, out))
	})

	for _, mode := range []fs.FileMode{0o755, 0o775, 0o600} {
		t.Run(fmt.Sprintf("rebuild keeps %04o", mode), func(t *testing.T) {
			// Arrange.
			outDir := t.TempDir()
			out := filepath.Join(outDir, "app")
			require.NoError(t, os.WriteFile(out, []byte("-- previous build\n"), 0o600))
			require.NoError(t, os.Chmod(out, mode))

			// Act.
			buildTo(t, dir, out)

			// Assert.
			assert.Equal(t, fmt.Sprintf("%04o", mode), perm(t, out))
			assert.Contains(t, readFile(t, out), `return require("main")`)
			assert.Equal(t, []string{"app"}, dirNames(t, outDir), "the build left files behind")
		})
	}
}

// TestCLI_ReadOnlyOutputRefused checks that a build does not replace an output
// file that is not writable, as os.Create did not. The rename that replaces
// the file needs only the directory to be writable, so the file is checked on
// its own: a bundle someone made read-only is not replaced behind their back.
func TestCLI_ReadOnlyOutputRefused(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}

	// Arrange.
	dir := writeProject(t)
	outDir := t.TempDir()
	out := filepath.Join(outDir, "bundle.lua")

	const previous = "-- frozen build\n"

	require.NoError(t, os.WriteFile(out, []byte(previous), 0o400))

	// Act.
	_, stderr, code := runCLI(t, append(entryArgs(dir), "--output", out)...)

	// Assert.
	assert.NotEqual(t, 0, code, "exit code")
	assert.Contains(t, stderr, "permission denied")
	assert.Equal(t, previous, readFile(t, out), "a read-only output was replaced")
	assert.Equal(t, "0400", perm(t, out))
	assert.Equal(t, []string{"bundle.lua"}, dirNames(t, outDir), "the build left files behind")
}

// TestCLI_OutputDevNull checks the README's CI recipe, `--strict --output
// /dev/null`: the build succeeds and the device is written to, never replaced
// by a regular file. When the device is also an input, writing to it must not
// be refused as overwriting one, since a device has no contents to destroy.
// The entry is that input here: go-config refuses an empty config file, so
// --config /dev/null fails before the output is looked at.
func TestCLI_OutputDevNull(t *testing.T) {
	t.Parallel()

	dir := writeProject(t)

	tests := []struct {
		name string
		args []string
	}{
		{"output only", entryArgs(dir)},
		{"device is also the entry", []string{"--entry", os.DevNull}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Act.
			args := slices.Concat(tt.args, []string{"--strict", "--output", os.DevNull})
			_, stderr, code := runCLI(t, args...)

			// Assert.
			assert.Equal(t, 0, code, stderr)

			info, err := os.Lstat(os.DevNull)
			require.NoError(t, err)
			assert.Equal(t, fs.ModeDevice|fs.ModeCharDevice, info.Mode().Type(),
				"%s must still be a character device", os.DevNull)
		})
	}
}

// TestCLI_OutputSymlink checks that an output path which is a symlink is
// written through, as os.Create did: the link stays a link and its target
// receives the bundle, including a target that does not exist yet.
func TestCLI_OutputSymlink(t *testing.T) {
	t.Parallel()

	dir := writeProject(t)

	tests := []struct {
		name     string
		existing bool // Whether the target of the link exists before the build.
	}{
		{"existing target", true},
		{"dangling link", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			outDir := t.TempDir()
			target := filepath.Join(outDir, "bundle-v1.lua")
			link := filepath.Join(outDir, "bundle.lua")

			if tt.existing {
				require.NoError(t, os.WriteFile(target, []byte("-- previous build\n"), 0o600))
			}

			require.NoError(t, os.Symlink("bundle-v1.lua", link))

			// Act.
			buildTo(t, dir, link)

			// Assert.
			dest, err := os.Readlink(link)
			require.NoError(t, err, "the output path must still be a symlink")
			assert.Equal(t, "bundle-v1.lua", dest)
			assert.Contains(t, readFile(t, target), `return require("main")`)
			assert.Equal(t, []string{"bundle-v1.lua", "bundle.lua"}, dirNames(t, outDir),
				"the build left files behind")
		})
	}
}

// TestCLI_OutputRefusesInputThroughSymlink checks that an output which is a
// symlink to a module is the module, and is refused like it.
func TestCLI_OutputRefusesInputThroughSymlink(t *testing.T) {
	t.Parallel()

	// Arrange.
	dir := writeProject(t)
	module := filepath.Join(dir, "module.lua")
	before := readFile(t, module)
	link := filepath.Join(t.TempDir(), "bundle.lua")
	require.NoError(t, os.Symlink(module, link))

	// Act.
	_, stderr, code := runCLI(t, append(entryArgs(dir), "--output", link)...)

	// Assert.
	assert.NotEqual(t, 0, code, "exit code")
	assert.Contains(t, stderr, "refusing to overwrite", "error message")
	assert.Equal(t, before, readFile(t, module), "module.lua bytes changed")

	dest, err := os.Readlink(link)
	require.NoError(t, err, "the output path must still be a symlink")
	assert.Equal(t, module, dest)
}

// TestCLI_FailedWriteKeepsPreviousOutput checks that when writing the bundle
// itself fails, the previous bundle survives untouched and no partial file is
// left behind. A zero file-size limit makes every write fail with EFBIG, as a
// full disk would; an in-place write would have truncated the old file by
// then. Go ignores SIGXFSZ, so the CLI sees the error instead of being killed.
func TestCLI_FailedWriteKeepsPreviousOutput(t *testing.T) {
	t.Parallel()

	// Arrange.
	dir := writeProject(t)
	outDir := t.TempDir()
	out := filepath.Join(outDir, "bundle.lua")

	const previous = "-- previous build\n"

	require.NoError(t, os.WriteFile(out, []byte(previous), 0o600))

	// $0 is the CLI and "$@" its arguments, so the limit binds the CLI alone.
	// The shell wrapper rules out luatest.RunCLI (which also captures output in
	// files the limit would refuse), so do what it does: run from an empty
	// directory with only PATH and HOME in the environment.
	script := `ulimit -f 0 && exec "$0" "$@"`
	args := slices.Concat([]string{"-c", script, luatest.CLIPath(t)}, entryArgs(dir), []string{"--output", out})

	// #nosec G204 -- the shell and its script are fixed; the CLI is the binary luatest built.
	cmd := exec.CommandContext(t.Context(), "/bin/sh", args...)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}

	var stderr bytes.Buffer // A pipe: the size limit would also truncate a file.

	cmd.Stderr = &stderr

	// Act.
	err := cmd.Run()

	// Assert.
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	require.True(t, ok, "the write must fail: %v", err)
	assert.Equal(t, 1, exitErr.ExitCode(), "exit code (-1 is a signal): %s", stderr.String())
	assert.Contains(t, stderr.String(), "write output")
	assert.Equal(t, previous, readFile(t, out), "the previous bundle was modified by a failed write")
	assert.Equal(t, []string{"bundle.lua"}, dirNames(t, outDir), "a failed write left files behind")
}
