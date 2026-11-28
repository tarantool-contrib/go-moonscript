package luatest_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/luatest"
)

// Tests of what the harness does when a test fails or is set up wrong. A
// failing test cannot be observed from inside its own process, so each of
// them runs a helper test in a child process of this test binary and checks
// how it ended.

// helperEnv names the variable that makes a helper test run: a test that
// exists to be run by another test in a child process. Its name does not
// start with LUAMERGE_: the config layer rejects unknown variables with that
// prefix.
const helperEnv = "LUATEST_HELPER"

// asHelper skips the test unless the process runs it as a helper.
func asHelper(t *testing.T) {
	t.Helper()

	if os.Getenv(helperEnv) != t.Name() {
		t.Skip("a helper, run by another test in a child process")
	}
}

// runHelper runs the helper test named name in a child process of this test
// binary, with env (KEY=VALUE pairs) added to the environment, and returns its
// exit status and output.
func runHelper(t *testing.T, name string, env ...string) (int, string) {
	t.Helper()

	// #nosec G204 G702 -- the test binary itself, running one of its own tests.
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+name+"$", "-test.count=1", "-test.v")
	cmd.Env = append(append(os.Environ(), helperEnv+"="+name), env...)

	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}

	exitErr, ok := errors.AsType[*exec.ExitError](err)
	require.True(t, ok, "run the helper %s: %v", name, err)

	return exitErr.ExitCode(), string(out)
}

// fixedBundle is a Bundler that produces bundle whatever the project.
func fixedBundle(bundle string) luatest.Bundler {
	return func(testing.TB, string) []byte { return []byte(bundle) }
}

// TestHelper_DifferentialMismatch is a differential test whose bundle prints
// something the original does not; see TestDifferential_ReportsDifferences.
func TestHelper_DifferentialMismatch(t *testing.T) {
	t.Parallel()
	asHelper(t)

	luatest.Differential(t, luatest.Case{
		Project: luatest.Project{"main.lua": "print(\"original\")\n"},
		Dir:     "",
		Entry:   "",
		Bundle:  fixedBundle("print(\"bundle\")\n"),
		Args:    nil,
		Options: defaults(),
		Check:   nil,
	})
}

// TestDifferential_ReportsDifferences checks that a differential test with
// the default check fails when the bundle does something else than the
// original. A check that compared nothing, or that ran the original twice,
// would leave every differential test green whatever the bundler did.
func TestDifferential_ReportsDifferences(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t) // Skips as the helper would, with no Lua on PATH.

	code, out := runHelper(t, "TestHelper_DifferentialMismatch")

	assert.Equal(t, 1, code, "the helper must fail\n%s", out)
	assert.Contains(t, out, "stdout differs")
	assert.Contains(t, out, "=== bundle: ")
}

// TestHelper_DifferentialEmptyBundle is a differential test whose bundler
// produces nothing; see TestDifferential_RefusesBadCases.
func TestHelper_DifferentialEmptyBundle(t *testing.T) {
	t.Parallel()
	asHelper(t)

	luatest.Differential(t, luatest.Case{
		Project: luatest.Project{"main.lua": ""},
		Dir:     "",
		Entry:   "",
		Bundle:  fixedBundle(""),
		Args:    nil,
		Options: defaults(),
		Check:   nil,
	})
}

// TestHelper_DifferentialProjectAndDir is a differential test that sets both
// Project and Dir; see TestDifferential_RefusesBadCases.
func TestHelper_DifferentialProjectAndDir(t *testing.T) {
	t.Parallel()
	asHelper(t)

	luatest.Differential(t, luatest.Case{
		Project: luatest.Project{"main.lua": "print(\"project\")\n"},
		Dir:     luatest.WriteProject(t, luatest.Project{"main.lua": "print(\"dir\")\n"}),
		Entry:   "",
		Bundle:  fixedBundle("print(\"dir\")\n"),
		Args:    nil,
		Options: defaults(),
		Check:   nil,
	})
}

// TestDifferential_RefusesBadCases checks that Differential fails a case it
// cannot judge instead of running it: an empty bundle, which runs and prints
// nothing like an empty original, and a case with both Project and Dir, of
// which one would be silently ignored.
func TestDifferential_RefusesBadCases(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t) // Skips as the helpers would, with no Lua on PATH.

	for helper, want := range map[string]string{
		"TestHelper_DifferentialEmptyBundle":   "luatest: the bundler produced an empty bundle",
		"TestHelper_DifferentialProjectAndDir": "luatest: Case sets both Project and Dir",
	} {
		t.Run(helper, func(t *testing.T) {
			t.Parallel()

			code, out := runHelper(t, helper)

			assert.Equal(t, 1, code, "the helper must fail\n%s", out)
			assert.Contains(t, out, want)
		})
	}
}

// TestDifferential_Entry checks that the original runs the script Case.Entry
// names: this project has no main.lua.
func TestDifferential_Entry(t *testing.T) {
	t.Parallel()

	luatest.Differential(t, luatest.Case{
		Project: luatest.Project{"app.lua": "print(\"app\")\n"},
		Dir:     "",
		Entry:   "app.lua",
		Bundle:  fixedBundle("print(\"app\")\n"),
		Args:    nil,
		Options: defaults(),
		Check: func(t *testing.T, orig, bundle luatest.Result) {
			t.Helper()
			luatest.AssertSame(t, orig, bundle)
			assert.Equal(t, "app.lua", orig.Script)
		},
	})
}

// TestForEachInterpreter_RunsEveryInterpreter checks that the subtest runs
// once per interpreter, one after another, in the order Interpreters returns
// them.
func TestForEachInterpreter_RunsEveryInterpreter(t *testing.T) {
	t.Parallel()

	interps := luatest.Interpreters(t)

	want := make([]string, 0, len(interps))
	for _, interp := range interps {
		want = append(want, interp.Name)
	}

	var ran []string

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		ran = append(ran, interp.Name)
	})

	assert.Equal(t, want, ran)
}

// TestHelper_Interpreters asks for the interpreters; see
// TestInterpreters_RequiredButMissing.
func TestHelper_Interpreters(t *testing.T) {
	t.Parallel()
	asHelper(t)

	luatest.Interpreters(t)
}

// TestInterpreters_RequiredButMissing checks that an interpreter required by
// EnvInterpreters but missing from PATH fails a test that asks for
// interpreters, so that CI cannot go green by running on fewer of them.
func TestInterpreters_RequiredButMissing(t *testing.T) {
	t.Parallel()

	code, out := runHelper(t, "TestHelper_Interpreters", luatest.EnvInterpreters+"=luatest-no-such-lua")

	assert.Equal(t, 1, code, "the helper must fail\n%s", out)
	assert.Contains(t, out, luatest.EnvInterpreters+" requires luatest-no-such-lua, which is not on PATH")
}

// TestHelper_CLIPath builds the CLI and names it; see
// TestCLIPath_RemovedByMain.
func TestHelper_CLIPath(t *testing.T) {
	t.Parallel()
	asHelper(t)

	t.Logf("CLI: %s", luatest.CLIPath(t))
}

// cliPathRe finds the CLI's path in TestHelper_CLIPath's output.
var cliPathRe = regexp.MustCompile(`CLI: (\S+)`)

// TestCLIPath_RemovedByMain checks that the CLI built for a test process is
// gone, with its directory, once the process has run its tests: Main removes
// it.
func TestCLIPath_RemovedByMain(t *testing.T) {
	t.Parallel()

	code, out := runHelper(t, "TestHelper_CLIPath")
	require.Equal(t, 0, code, out)

	match := cliPathRe.FindStringSubmatch(out)
	require.NotNil(t, match, "the helper names the CLI it built\n%s", out)
	assert.NoFileExists(t, match[1])
	assert.NoDirExists(t, filepath.Dir(match[1]))
}
