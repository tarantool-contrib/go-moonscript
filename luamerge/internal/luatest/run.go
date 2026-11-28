package luatest

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// DefaultLuaPath is the LUA_PATH of every run unless RunOptions says otherwise.
//
// It puts the bundler's default templates (`?.lua;?/init.lua`) first, relative
// to the working directory, and keeps the interpreter's built-in path after
// them (`;;`). Without it the original and the bundle disagree for a reason
// that has nothing to do with the bundler: Lua 5.1, 5.2 and LuaJIT have no
// `./?/init.lua` in their built-in path, so a project whose module lives in
// foo/init.lua fails natively there while its bundle, built with that
// template, works. Every interpreter reads LUA_PATH when no versioned
// LUA_PATH_5_x is set, and the scrubbed environment sets none; Tarantool
// reads it too and appends its own directories.
const DefaultLuaPath = "./?.lua;./?/init.lua;;"

// DefaultTimeout bounds every interpreter run. A require cycle can recurse
// until the C stack overflows, and a broken bundle can loop forever; neither
// may hang the suite.
const DefaultTimeout = 10 * time.Second

// errTimeout is the cause when a run does not finish within its timeout.
var errTimeout = errors.New("timed out")

// RunOptions tunes an interpreter run. The zero value gives the defaults.
type RunOptions struct {
	// LuaPath is the LUA_PATH of the run; "" means DefaultLuaPath. ";;" gives
	// the interpreter's built-in path.
	LuaPath string
	// Env holds extra KEY=VALUE pairs. The environment is otherwise scrubbed to
	// PATH, HOME and LUA_PATH; a key given here replaces those.
	Env []string
	// Timeout bounds the run; 0 means DefaultTimeout.
	Timeout time.Duration
}

// Result is what one interpreter run did. The fields are raw, so a test can
// assert more sharply than the comparisons in this package do.
type Result struct {
	// Interp is the interpreter that ran.
	Interp Interpreter
	// Dir is the working directory of the run.
	Dir string
	// Script is the script as passed to the interpreter.
	Script string
	// Args are the script's arguments.
	Args []string
	// Stdout is everything the run wrote to stdout.
	Stdout string
	// Stderr is everything the run wrote to stderr.
	Stderr string
	// ExitCode is the exit status; -1 when the process died of a signal.
	ExitCode int
}

// String describes the run and everything it printed, for failure messages.
func (r Result) String() string {
	return fmt.Sprintf("%s\n  in %s\n  exit %d\n--- stdout:\n%s--- stderr:\n%s",
		r.command(), r.Dir, r.ExitCode, withNewline(r.Stdout), withNewline(r.Stderr))
}

// command is the command line of the run, as the interpreter saw it.
func (r Result) command() string {
	return strings.Join(append([]string{r.Interp.Name, r.Script}, r.Args...), " ")
}

func withNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}

	return s + "\n"
}

// Run runs `<interpreter> script args...` in dir with a scrubbed environment
// (see RunOptions) and returns what it did. It fails the test when the
// interpreter cannot be started or does not finish within the timeout: a
// timeout is never a result, since a hanging original and a hanging bundle
// would compare equal.
func Run(tb testing.TB, interp Interpreter, dir string, opts RunOptions, script string, args ...string) Result {
	tb.Helper()

	result, err := run(tb.Context(), interp, dir, opts, script, args)
	if err != nil {
		tb.Fatalf("luatest: %v\n%s", err, result)
	}

	return result
}

// RunBundle writes bundle as bundle.lua into a fresh directory that holds
// nothing else, and runs it there: a module the bundle does not carry cannot
// be loaded from disk.
func RunBundle(tb testing.TB, interp Interpreter, bundle []byte, opts RunOptions, args ...string) Result {
	tb.Helper()

	dir := tb.TempDir()
	require.NoError(tb, os.WriteFile(filepath.Join(dir, "bundle.lua"), bundle, fileMode))

	return Run(tb, interp, dir, opts, "bundle.lua", args...)
}

func run(
	ctx context.Context, interp Interpreter, dir string, opts RunOptions, script string, args []string,
) (Result, error) {
	result := Result{
		Interp: interp, Dir: dir, Script: script, Args: args, Stdout: "", Stderr: "", ExitCode: 0,
	}
	timeout := cmp.Or(opts.Timeout, DefaultTimeout)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- the interpreter was found on PATH by discover; the script and its arguments come from the test.
	cmd := exec.CommandContext(ctx, interp.Path, append([]string{script}, args...)...)
	// PUC Lua and LuaJIT prefix an uncaught error with argv[0]. Make that the
	// interpreter's name whatever its path, so NormalizeStderr knows it.
	cmd.Args[0] = interp.Name
	cmd.Dir = dir

	cmd.Env = append(scrubbedEnv(), "LUA_PATH="+cmp.Or(opts.LuaPath, DefaultLuaPath))
	cmd.Env = append(cmd.Env, opts.Env...)

	out, err := captureOutput(cmd)
	if err != nil {
		return result, err
	}

	defer out.close()

	runErr := cmd.Run()

	result.Stdout, result.Stderr, err = out.read()
	if err != nil {
		return result, err
	}

	if ctx.Err() != nil {
		return result, fmt.Errorf("%s did not finish within %s: %w", result.command(), timeout, errTimeout)
	}

	if runErr != nil {
		exitErr, ok := errors.AsType[*exec.ExitError](runErr)
		if !ok {
			return result, fmt.Errorf("run %s: %w", result.command(), runErr)
		}

		result.ExitCode = exitErr.ExitCode()
	}

	return result, nil
}

// output is where a child process writes its stdout and stderr: two files,
// not pipes. With a pipe, Wait also waits until every process holding its
// write end has closed it: a process the child leaves behind, or under load
// the child of another test caught between fork and exec, can hold it after
// the child has exited, and the run stalls or fails although it is over.
// With files there is nothing but the child itself to wait for.
type output struct {
	stdout *os.File
	stderr *os.File
}

// captureOutput points the stdout and stderr of cmd at fresh files. Close the
// output once cmd has finished and been read.
func captureOutput(cmd *exec.Cmd) (*output, error) {
	stdout, err := anonymousFile()
	if err != nil {
		return nil, err
	}

	stderr, err := anonymousFile()
	if err != nil {
		_ = stdout.Close()

		return nil, err
	}

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	return &output{stdout: stdout, stderr: stderr}, nil
}

// read returns what the child wrote to stdout and stderr.
func (o *output) read() (string, string, error) {
	stdout, err := readAll(o.stdout)
	if err != nil {
		return "", "", err
	}

	stderr, err := readAll(o.stderr)
	if err != nil {
		return "", "", err
	}

	return stdout, stderr, nil
}

// close closes both files, which are already unlinked.
func (o *output) close() {
	_ = o.stdout.Close()
	_ = o.stderr.Close()
}

// anonymousFile creates a temporary file and unlinks it at once, so nothing is
// left behind even when the test process dies.
func anonymousFile() (*os.File, error) {
	file, err := os.CreateTemp("", "luatest-output-*")
	if err != nil {
		return nil, fmt.Errorf("create an output file: %w", err)
	}

	err = os.Remove(file.Name())
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("unlink the output file: %w", err)
	}

	return file, nil
}

// readAll reads file from its start: the child shares the file offset and
// leaves it at the end of what it wrote.
func readAll(file *os.File) (string, error) {
	_, err := file.Seek(0, io.SeekStart)
	if err != nil {
		return "", fmt.Errorf("rewind the output file: %w", err)
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read the output file: %w", err)
	}

	return string(data), nil
}

// scrubbedEnv is the environment a child process starts from: the parent's
// PATH and HOME and nothing else, so no LUAMERGE_* variable (configuration)
// or LUA_* variable (LUA_INIT, LUA_PATH_5_4, ...) of the caller's shell can
// change a result.
func scrubbedEnv() []string {
	var env []string

	for _, key := range []string{"PATH", "HOME"} {
		value, ok := os.LookupEnv(key)
		if ok {
			env = append(env, key+"="+value)
		}
	}

	return env
}
