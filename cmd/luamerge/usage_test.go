package main

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge"
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

// warningPosition matches the position a warning line starts with.
var warningPosition = regexp.MustCompile(`^warning: (.+?):(\d+): `)

// TestCLI_WarningPosition checks how warnings are printed: a warning with a
// file starts with its file:line, which its message does not repeat, and one
// without a file has no position at all. They used to read
// "main.lua:4: unresolved require "missing" at line 4" and, for --include,
// "warning: :0: include package ...".
func TestCLI_WarningPosition(t *testing.T) {
	t.Parallel()

	// Arrange: one of each warning a source file can cause.
	dir := luatest.WriteProject(t, luatest.Project{
		"main.lua": "local name = \"x\"\n" +
			"require(name)\n" + // Dynamic.
			"require(\"cjson\")\n" + // Skipped.
			"require(\"missing\")\n" + // Unresolved.
			"local r = require\n" + // Indirect, where require is used other than by a call.
			"local ok = pcall(require, \"optional\")\n" + // Unresolved, in protected mode.
			"require(\"csv\")\n", // A module of the project named like Tarantool's built-in.
		"csv.lua": "return {}\n",
		"bit.lua": "return {}\n",
	})
	mainLua, err := filepath.EvalSymlinks(filepath.Join(dir, "main.lua"))
	require.NoError(t, err)

	// Act.
	_, stderr, code := runCLIIn(t, dir, "--entry", "main.lua", "--skip", "cjson", "--include", "nothere",
		"--include", "debug", "--include", "bit")

	// Assert.
	require.Equal(t, 0, code, stderr)

	var warnings []string

	for line := range strings.Lines(stderr) {
		if strings.HasPrefix(line, "warning: ") {
			warnings = append(warnings, strings.TrimSuffix(line, "\n"))
		}
	}

	require.GreaterOrEqual(t, len(warnings), 4, "%s", stderr)

	for _, warning := range warnings {
		match := warningPosition.FindStringSubmatch(warning)
		if match == nil {
			assert.NotRegexp(t, `^warning: :`, warning, "an empty position")

			continue
		}

		assert.NotEqual(t, "0", match[2], "line 0: %s", warning)
		assert.NotContains(t, warning[len(match[0]):], "line "+match[2], "the line is repeated: %s", warning)
	}

	for _, want := range []string{
		"warning: " + mainLua + ":2: dynamic require, cannot resolve",
		"warning: " + mainLua + ":3: skipped package \"cjson\"",
		"warning: " + mainLua + ":4: unresolved require \"missing\": ",
		"warning: include package \"nothere\" not found: ",
		"warning: include package \"debug\" skipped: ",
		"warning: " + mainLua + ":6: unresolved require \"optional\" in protected mode (pcall or xpcall): ",
		"warning: " + mainLua + ":7: require \"csv\": Tarantool loads its built-in module \"csv\" instead",
		"warning: include package \"bit\": LuaJIT and Tarantool load their built-in module \"bit\" instead",
	} {
		found := slices.ContainsFunc(warnings, func(warning string) bool { return strings.HasPrefix(warning, want) })
		assert.True(t, found, "no warning starts with %q:\n%s", want, stderr)
	}
}

// TestWarningText checks the position printed with a warning: none without
// a file, the file alone without a line.
func TestWarningText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file string
		line int
		want string
	}{
		{"/p/main.lua", 4, "/p/main.lua:4: message"},
		{"/p/main.lua", 0, "/p/main.lua: message"},
		{"", 0, "message"},
	}

	for _, tt := range tests {
		w := luamerge.Warning{
			Kind: luamerge.WarnUnresolved, Module: "m", File: tt.file, Line: tt.line, Message: "message",
		}
		assert.Equal(t, tt.want, warningText(w))
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
