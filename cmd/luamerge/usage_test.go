package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/luatest"
)

// Tests for how the CLI reads its command line and reports what it did.

// usageExitCode is the exit status of a command-line mistake, as pflag uses
// it for an unknown flag. Spelled out rather than taken from the code, so
// that changing it fails a test.
const usageExitCode = 2

// TestCLI_PositionalArguments checks that a positional argument is refused.
// They used to be ignored: `--minify false` minified, since a boolean flag
// takes a value only after "=" and "false" was an argument of its own, and
// `luamerge main.lua` complained that --entry was missing.
func TestCLI_PositionalArguments(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{"main.lua": "-- COMMENT_MARK\nprint(\"hi\")\n"})

	tests := []struct {
		name string
		args []string
		want []string // Substrings of the error.
	}{
		{"boolean value apart", []string{"--entry", "main.lua", "--minify", "false"},
			[]string{`unexpected argument "false"`, "--minify=false"}},
		{"boolean value in another spelling", []string{"--debug", "1", "--entry", "main.lua"},
			[]string{`unexpected argument "1"`, "--debug=1"}},
		{"entry without its flag", []string{"main.lua"},
			[]string{`unexpected argument "main.lua"`, "--entry main.lua"}},
		{"stray word", []string{"--entry", "main.lua", "extra"}, []string{`unexpected argument "extra"`}},
		{"after the terminator", []string{"--entry", "main.lua", "--", "--minify"},
			[]string{`unexpected argument "--minify"`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := runCLIIn(t, dir, tt.args...)

			assert.Equal(t, usageExitCode, code, "exit code")
			assert.Empty(t, stdout)

			for _, want := range tt.want {
				assert.Contains(t, stderr, want)
			}

			assert.NotContains(t, stderr, "is required")
		})
	}
}

// TestCLI_PositionalArgumentsHint checks that the "=" form is suggested only
// for a word standing where a boolean flag's value would be.
func TestCLI_PositionalArgumentsHint(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{"main.lua": "print(\"hi\")\n"})

	tests := []struct {
		name string
		args []string
	}{
		{"not a boolean", []string{"--entry", "main.lua", "--minify", "maybe"}},
		{"value already given", []string{"--entry", "main.lua", "--minify=false", "true"}},
		{"after a string flag's value", []string{"--entry", "main.lua", "false"}},
		{"after the terminator", []string{"--entry", "main.lua", "--", "false"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, stderr, code := runCLIIn(t, dir, tt.args...)

			assert.Equal(t, usageExitCode, code, "exit code")
			assert.Contains(t, stderr, "unexpected argument")
			assert.NotContains(t, stderr, "=")
		})
	}
}

// TestCLI_EmptyOutput checks that an empty --output means what its help says:
// the output of the configuration, or the default, stdout. It used to name a
// file "", and the build failed renaming its temporary file to "".
func TestCLI_EmptyOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string   // luamerge.yaml; "" writes none.
		env  []string // KEY=VALUE pairs.
		file string   // The output expected; "" means stdout.
	}{
		{"default", "", nil, ""},
		{"config file", "output: out/bundle.lua\n", nil, "out/bundle.lua"},
		{"environment", "", []string{"LUAMERGE_OUTPUT=env.lua"}, "env.lua"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			project := luatest.Project{"main.lua": "print(\"hi\")\n"}
			if tt.yaml != "" {
				project["luamerge.yaml"] = tt.yaml
			}

			dir := luatest.WriteProject(t, project)

			// Act.
			result := luatest.RunCLI(t, dir, tt.env, "--entry", "main.lua", "--output", "")

			// Assert.
			require.Equal(t, 0, result.ExitCode, result.Stderr)

			bundle := result.Stdout
			if tt.file != "" {
				assert.Empty(t, result.Stdout)

				bundle = readFile(t, filepath.Join(dir, tt.file))
			}

			assert.Contains(t, bundle, entryHeader("main"))
		})
	}
}

// TestCLI_StringFlagTakesBooleanWord checks that a word reading as a boolean
// is still the value of a flag that takes a string.
func TestCLI_StringFlagTakesBooleanWord(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{
		"main.lua": "require(\"m\")\n",
		"m.lua":    "return 1\n",
	})

	stdout, stderr, code := runCLIIn(t, dir, "--entry", "main.lua", "--package-prefix", "false")

	// The prefix shows in the name generated for the module m.
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, preloaded("false.m"))
}
