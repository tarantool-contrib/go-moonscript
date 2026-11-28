package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the way the CLI puts the bundle on disk that hold on every
// platform. The guards that need file modes, devices and symlinks are in
// output_unix_test.go.

// entryCode is the last line of the entry of writeProject's project. The
// entry's code comes last in a bundle, so a file that holds it holds a whole
// bundle.
const entryCode = "print(m.greet())\n"

// entryArgs points the CLI at the project in dir; the caller adds --output.
func entryArgs(dir string) []string {
	return []string{"--entry", filepath.Join(dir, "main.lua"), "--root", dir}
}

// writeBrokenProject creates a project whose entry requires a module that does
// not exist, so a --strict build of it fails.
func writeBrokenProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"), []byte("require(\"missing\")\n"), 0o600))

	return dir
}

// readFile returns the contents of path, failing the test if it cannot be read.
func readFile(t *testing.T, path string) string {
	t.Helper()

	// #nosec G304 -- path is a file the calling test created under t.TempDir().
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(data)
}

// dirNames lists the names in dir, sorted, so a test can assert that a run
// left nothing behind: no temporary files, no stray outputs.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

// TestCLI_OutputRefusesInputs checks that an --output which is one of the
// bundle's inputs is refused and that nothing in the project changes. The
// output is spelled the ways a user might: relative to the project like the
// entry, absolute while the entry is relative, and as a hard link, which has a
// name of its own. No comparison of path strings refuses all of them; file
// identity does. The output used to be truncated before the sources were
// read, so the file was replaced by a bundle of an empty file, with exit 0.
func TestCLI_OutputRefusesInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output func(dir string) string // The --output value.
	}{
		{"entry", func(string) string { return "main.lua" }},
		{"entry by absolute path", func(dir string) string { return filepath.Join(dir, "main.lua") }},
		{"module", func(string) string { return "module.lua" }},
		{"hard link to a module", func(string) string { return "alias.lua" }},
		{"config file", func(string) string { return "luamerge.yaml" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange: luamerge.yaml is loaded by default from the working directory.
			dir := writeProject(t)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "luamerge.yaml"), []byte("strict: false\n"), 0o600))
			require.NoError(t, os.Link(filepath.Join(dir, "module.lua"), filepath.Join(dir, "alias.lua")))

			output := tt.output(dir)

			victim := output
			if !filepath.IsAbs(victim) {
				victim = filepath.Join(dir, victim)
			}

			before := readFile(t, victim)
			names := dirNames(t, dir)

			// Act.
			stdout, stderr, code := runCLIIn(t, dir, "--entry", "main.lua", "--output", output)

			// Assert.
			assert.NotEqual(t, 0, code, "exit code")
			assert.Contains(t, stderr, "refusing to overwrite", "error message")
			assert.Empty(t, stdout)
			assert.Equal(t, before, readFile(t, victim), "%s bytes changed", output)
			assert.Equal(t, names, dirNames(t, dir), "the project directory gained or lost files")
		})
	}
}

// TestCLI_FailedBuildKeepsPreviousOutput checks that a failed build leaves the
// previous bundle intact byte for byte. The output used to be truncated before
// the build ran, so a failed build left a 0-byte file that make then took for
// up to date.
func TestCLI_FailedBuildKeepsPreviousOutput(t *testing.T) {
	t.Parallel()

	// Arrange: a good build, then an entry that requires a missing module.
	dir := writeProject(t)
	outDir := t.TempDir()
	out := filepath.Join(outDir, "bundle.lua")
	args := append(entryArgs(dir), "--strict", "--output", out)

	_, stderr, code := runCLI(t, args...)
	require.Equal(t, 0, code, "first build: %s", stderr)

	good := readFile(t, out)
	require.Contains(t, good, entryCode)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.lua"), []byte("require(\"missing\")\n"), 0o600))

	// Act.
	_, stderr, code = runCLI(t, args...)

	// Assert.
	assert.NotEqual(t, 0, code, "the second build must fail")
	assert.Contains(t, stderr, "unresolved")
	assert.Equal(t, good, readFile(t, out), "the previous bundle was modified by a failed build")
	assert.Equal(t, []string{"bundle.lua"}, dirNames(t, outDir), "a failed build left files behind")
}

// TestCLI_FailedBuildCreatesNoDirectories checks that the output's missing
// parent directories are created only once the build has succeeded.
func TestCLI_FailedBuildCreatesNoDirectories(t *testing.T) {
	t.Parallel()

	// Arrange.
	dir := writeBrokenProject(t)
	outRoot := t.TempDir()
	out := filepath.Join(outRoot, "dist", "lua", "bundle.lua")

	// Act.
	_, _, code := runCLI(t, append(entryArgs(dir), "--strict", "--output", out)...)

	// Assert.
	assert.NotEqual(t, 0, code, "the build must fail")
	assert.NoDirExists(t, filepath.Join(outRoot, "dist"), "a failed build created the output directory")
	assert.Empty(t, dirNames(t, outRoot), "a failed build left files behind")
}

// TestCLI_OutputCreatesDirectories checks that a successful build still
// creates the output's missing parent directories, as the README's
// `--output dist/bundle.lua` expects.
func TestCLI_OutputCreatesDirectories(t *testing.T) {
	t.Parallel()

	// Arrange.
	dir := writeProject(t)
	out := filepath.Join(t.TempDir(), "dist", "lua", "bundle.lua")

	// Act.
	_, stderr, code := runCLI(t, append(entryArgs(dir), "--output", out)...)

	// Assert.
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, readFile(t, out), entryCode)
}

// TestCLI_StdoutEmptyOnFailedBuild checks that with --output - a failed build
// prints nothing on stdout, so `luamerge ... | lua -` never runs half a bundle.
func TestCLI_StdoutEmptyOnFailedBuild(t *testing.T) {
	t.Parallel()

	// Arrange.
	dir := writeBrokenProject(t)

	// Act.
	stdout, stderr, code := runCLI(t, append(entryArgs(dir), "--strict", "--output", "-")...)

	// Assert.
	assert.NotEqual(t, 0, code, "the build must fail")
	assert.Contains(t, stderr, "unresolved")
	assert.Empty(t, stdout, "a failed build printed to stdout")
}
