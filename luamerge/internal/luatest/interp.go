package luatest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// EnvInterpreters names the environment variable that makes interpreters
// mandatory: a comma-separated list of names, each of which must be on PATH,
// e.g. "lua5.1,lua5.4,luajit". Without it a machine with no Lua at all skips
// the Lua-executing tests; with it a missing interpreter fails them, so CI
// cannot go green by running nothing. Set but naming nothing is an error too,
// so an empty CI variable cannot turn into a silent skip.
//
// The name does not start with LUAMERGE_ on purpose: the config layer rejects
// an unknown LUAMERGE_* variable, and tests that load the configuration in
// process would fail on it.
const EnvInterpreters = "LUATEST_INTERPRETERS"

// errProbe is the cause when an interpreter cannot run the probe script.
var errProbe = errors.New("the probe script did not report a version")

// discovered is the discovery of the test process: interpreters are looked up
// and probed once, by the first test that asks for them.
//
//nolint:gochecknoglobals // one discovery per test process, shared by every test
var discovered = sync.OnceValue(func() discovery {
	required, set := os.LookupEnv(EnvInterpreters)

	return discover(required, set, exec.LookPath, filepath.EvalSymlinks, probe)
})

// Interpreter is a Lua interpreter found on PATH.
type Interpreter struct {
	// Name is the name it was found under, e.g. "lua5.1", "luajit", "tarantool".
	// Subtests are named after it, and it is the argv[0] of every run.
	Name string
	// Path is where PATH lookup found it.
	Path string
	// Version is what the interpreter reports about itself, e.g. "Lua 5.1",
	// "LuaJIT 2.1.1761786044", "Tarantool 3.9.0-entrypoint-46-gddac17c84e".
	Version string
}

// String describes the interpreter for logs: name, version and path.
func (interp Interpreter) String() string {
	return fmt.Sprintf("%s (%s, %s)", interp.Name, interp.Version, interp.Path)
}

// Interpreters returns every Lua interpreter on PATH, one per distinct binary,
// and logs them. With none found it skips the test, unless EnvInterpreters is
// set. It fails the test when an interpreter EnvInterpreters requires is
// missing, when the variable names nothing, or when an interpreter on PATH
// cannot run a trivial script. Discovery runs once per test process.
func Interpreters(tb testing.TB) []Interpreter {
	tb.Helper()

	found := discovered()
	if len(found.problems) > 0 {
		tb.Fatalf("luatest: %s", strings.Join(found.problems, "\n"))
	}

	if len(found.interps) == 0 {
		tb.Skip(found.skip)
	}

	names := make([]string, 0, len(found.interps))
	for _, interp := range found.interps {
		names = append(names, interp.String())
	}

	tb.Logf("Lua interpreters: %s", strings.Join(names, "; "))

	return slices.Clone(found.interps)
}

// candidateNames are looked up on PATH in this order. The versioned names come
// before a plain `lua`: when `lua` is only another name for one of them
// (Ubuntu's alternatives symlink, Homebrew's lua5.5 -> lua) the deduplicated
// entry keeps the name that says which Lua it is.
func candidateNames() []string {
	return []string{"lua5.1", "lua5.2", "lua5.3", "lua5.4", "lua5.5", "luajit", "tarantool", "lua"}
}

// discovery is what discover found.
type discovery struct {
	interps []Interpreter
	// skip says why there is nothing to run on; set when interps is empty and
	// nothing is required.
	skip string
	// problems fail every test that asks for interpreters.
	problems []string
}

// discover finds the interpreters. required is the value of EnvInterpreters
// and set whether it is present at all: present but naming nothing is an
// error, not "no requirement", because an unset CI variable must not turn
// into a silent skip.
func discover(
	required string, set bool,
	lookPath, resolve func(name string) (string, error),
	probe func(interp Interpreter) (string, error),
) discovery {
	var found discovery

	var want []string

	if set {
		for name := range strings.SplitSeq(required, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				want = append(want, name)
			}
		}

		if len(want) == 0 {
			found.problems = append(found.problems,
				fmt.Sprintf("%s=%q names no interpreter", EnvInterpreters, required))

			return found
		}
	}

	names := candidateNames()
	for _, name := range want {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	onPath := make(map[string]bool)
	seen := make(map[string]bool)

	for _, name := range names {
		path, err := lookPath(name)
		if err != nil {
			continue
		}

		resolved, err := resolve(path)
		if err != nil {
			found.problems = append(found.problems, fmt.Sprintf("resolve %s (%s): %v", name, path, err))

			continue
		}

		onPath[name] = true

		if seen[resolved] {
			continue
		}

		seen[resolved] = true
		interp := Interpreter{Name: name, Path: path, Version: ""}

		version, err := probe(interp)
		if err != nil {
			found.problems = append(found.problems,
				fmt.Sprintf("%s (%s) cannot run a trivial script: %v", name, path, err))

			continue
		}

		interp.Version = version
		found.interps = append(found.interps, interp)
	}

	for _, name := range want {
		if !onPath[name] {
			found.problems = append(found.problems,
				fmt.Sprintf("%s requires %s, which is not on PATH", EnvInterpreters, name))
		}
	}

	if len(found.interps) == 0 && !set {
		found.skip = fmt.Sprintf("no Lua interpreter on PATH (looked for %s); set %s to make that a failure",
			strings.Join(candidateNames(), ", "), EnvInterpreters)
	}

	return found
}

// probeScript writes the interpreter's own idea of its version. Tarantool is
// LuaJIT underneath, so it is asked first.
const probeScript = `io.write(_TARANTOOL and ("Tarantool " .. tostring(_TARANTOOL))` +
	` or (jit and jit.version) or _VERSION)` + "\n"

// probe runs probeScript, which proves the interpreter can run a script the
// way the tests will, and returns the version it reports.
func probe(interp Interpreter) (string, error) {
	dir, err := os.MkdirTemp("", "luatest-probe-*")
	if err != nil {
		return "", fmt.Errorf("create the probe directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	err = os.WriteFile(filepath.Join(dir, "probe.lua"), []byte(probeScript), fileMode)
	if err != nil {
		return "", fmt.Errorf("write the probe script: %w", err)
	}

	defaults := RunOptions{LuaPath: "", Env: nil, Timeout: 0}

	result, err := run(context.Background(), interp, dir, defaults, "probe.lua", nil)
	if err != nil {
		return "", err
	}

	if result.ExitCode != 0 || result.Stdout == "" {
		return "", fmt.Errorf("%w: exit %d, stdout %q, stderr %q",
			errProbe, result.ExitCode, result.Stdout, result.Stderr)
	}

	return result.Stdout, nil
}
