package luatest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// cliPackage is the command CLIPath builds.
const cliPackage = "github.com/tarantool/go-luamerge/cmd/luamerge"

// cliTimeout bounds a CLI run; bundling a test project takes milliseconds.
const cliTimeout = time.Minute

// buildTimeout bounds building the CLI.
const buildTimeout = 5 * time.Minute

// cliBuild is the CLI binary of the test process: built by the first CLIPath,
// removed by Main.
type cliBuild struct {
	once sync.Once
	dir  string
	path string
	err  error
}

// The CLI binary is shared by every test of the process, and only Main knows
// when the last of them has finished.
//
//nolint:gochecknoglobals // one CLI build per test process, shared by every test
var (
	// mainInstalled is set by Main: only then is the built binary removed
	// when the test process ends.
	mainInstalled atomic.Bool
	cli           cliBuild
)

// Main runs the package's tests and then removes the CLI binary built for
// them. Every package that uses CLIPath, RunCLI or CLI must call it:
//
//	func TestMain(m *testing.M) { luatest.Main(m) }
func Main(m *testing.M) {
	mainInstalled.Store(true)

	code := m.Run()

	if cli.dir != "" {
		_ = os.RemoveAll(cli.dir)
	}

	os.Exit(code)
}

// CLIPath returns the luamerge binary, built from this checkout once per test
// process.
func CLIPath(tb testing.TB) string {
	tb.Helper()

	if !mainInstalled.Load() {
		tb.Fatal("luatest: the CLI is built per test process and removed by luatest.Main; " +
			"add `func TestMain(m *testing.M) { luatest.Main(m) }` to this package")
	}

	cli.once.Do(cli.build)

	if cli.err != nil {
		tb.Fatalf("luatest: %v", cli.err)
	}

	return cli.path
}

// build builds the CLI into a fresh temporary directory.
func (b *cliBuild) build() {
	dir, err := os.MkdirTemp("", "luatest-cli-*")
	if err != nil {
		b.err = fmt.Errorf("create a directory for the CLI: %w", err)

		return
	}

	b.dir = dir
	path := filepath.Join(dir, "luamerge")

	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()

	// #nosec G204 -- "go" and the build arguments are fixed by this package, not attacker input.
	out, err := exec.CommandContext(ctx, "go", "build", "-o", path, cliPackage).CombinedOutput()
	if err != nil {
		b.err = fmt.Errorf("go build %s: %w\n%s", cliPackage, err, out)

		return
	}

	b.path = path
}

// CLIResult is what one CLI run did.
type CLIResult struct {
	// Stdout is everything the CLI wrote to stdout.
	Stdout string
	// Stderr is everything the CLI wrote to stderr.
	Stderr string
	// ExitCode is the exit status.
	ExitCode int
}

// RunCLI runs the luamerge CLI in dir with args. The environment is scrubbed
// to PATH and HOME plus env (KEY=VALUE pairs), so no LUAMERGE_* variable of
// the caller's shell reaches the config layer unless a test passes it.
func RunCLI(tb testing.TB, dir string, env []string, args ...string) CLIResult {
	tb.Helper()

	bin := CLIPath(tb)

	ctx, cancel := context.WithTimeout(tb.Context(), cliTimeout)
	defer cancel()

	// #nosec G204 -- bin is the binary CLIPath built from this checkout; the arguments come from the test.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir

	cmd.Env = append(scrubbedEnv(), env...)

	out, err := captureOutput(cmd)
	if err != nil {
		tb.Fatalf("luatest: %v", err)
	}

	defer out.close()

	runErr := cmd.Run()

	stdout, stderr, err := out.read()
	if err != nil {
		tb.Fatalf("luatest: %v", err)
	}

	result := CLIResult{Stdout: stdout, Stderr: stderr, ExitCode: 0}

	if ctx.Err() != nil {
		tb.Fatalf("luatest: luamerge %s did not finish within %s\nstderr:\n%s",
			strings.Join(args, " "), cliTimeout, result.Stderr)
	}

	if runErr != nil {
		exitErr, ok := errors.AsType[*exec.ExitError](runErr)
		if !ok {
			tb.Fatalf("luatest: run luamerge %s: %v", strings.Join(args, " "), runErr)
		}

		result.ExitCode = exitErr.ExitCode()
	}

	return result
}

// CLI returns a Bundler that runs the luamerge CLI in the project directory
// with args (see RunCLI) and takes the bundle from its stdout, so neither args
// nor a luamerge.yaml in the project may send the output elsewhere. The CLI
// must exit 0; its stderr (warnings) is logged.
func CLI(args ...string) Bundler {
	return func(tb testing.TB, dir string) []byte {
		tb.Helper()

		result := RunCLI(tb, dir, nil, args...)
		if result.ExitCode != 0 {
			tb.Fatalf("luatest: luamerge %s: exit %d\n%s", strings.Join(args, " "), result.ExitCode, result.Stderr)
		}

		if result.Stderr != "" {
			tb.Logf("luamerge %s:\n%s", strings.Join(args, " "), result.Stderr)
		}

		if result.Stdout == "" {
			tb.Fatalf("luatest: luamerge %s wrote no bundle to stdout", strings.Join(args, " "))
		}

		return []byte(result.Stdout)
	}
}
