package main

// Behavioral tests of the moon command against the reference moon (upstream
// bin/moon v0.8.0 on PUC Lua 5.1.5, and run by LuaJIT 2.1 for the LuaJIT
// variants, which run the port with MOON_LUA=luajit). The reference outputs
// are recorded by scripts/gen-moon-runner-ref.sh (needs Docker) in ref/ and
// ref_luajit/; these tests run without it.
//
// TestMoonRunner runs every case of testdata/moon_runner/cases.tsv (scripts
// written for this suite: classes, modules through package.moonpath, script
// arguments, the moon standard library, runtime errors and their rewritten
// tracebacks, syntax errors in required modules, the CLI flags) and
// TestMoonSmoke runs the upstream scripts listed in
// testdata/moon_smoke/scripts.txt (docs examples, spec inputs);
// TestMoonRunnerLuaJIT and TestMoonSmokeLuaJIT run the same in the LuaJIT
// dialect. Each runs
// the test binary itself as moon (TestMain) in the case's directory and
// compares exit status, stdout and stderr after normalize, which removes
// the differences that come from the VM and the installation rather than
// from moon:
//
//  1. Installation paths: the reference loads its library modules from
//     /usr/local/share/lua/5.1 (moonscript/base.lua:91: ...); the port's
//     embedded modules are named relative to that directory. The prefix is
//     removed from the reference output; the line numbers must match (the
//     embedded modules are byte-identical to the reference's).
//  2. moonscript/errors.lua line numbers: the port's errors module differs
//     from upstream (no lpeg), so its line numbers become "N".
//  3. The driver: errors escaping bin/moon (-d mode, or a failure inside the
//     traceback rewriter) are reported by lua5.1 as "/usr/bin/lua5.1: msg"
//     (or by luajit as "/usr/bin/luajit: msg") and by the port as "moon:
//     msg"; the traceback frames from the driver
//     itself (".../bin/moon:106: ..." vs "moon:96: ...") and everything
//     below them are replaced by "\t<moon driver>". The frames of the
//     script, including "in function 'moonscript_chunk'", are kept.
//  4. Addresses: "table: 0x55d0c5a4c2a0" -> "table: 0xADDR".
//  5. Module search lists: "module 'x' not found:" lists the files tried,
//     which depend on package.path and cpath (the reference also has a
//     LuaRocks loader: "No LuaRocks module found for x"); the "no file"
//     lines and the LuaRocks text are dropped, the "no field
//     package.preload" line is kept.
//  6. Lua syntax errors in compiled code (only upstream examples using
//     LuaJIT/Lua 5.2 syntax have them): gopher-lua's parser words them
//     differently and sometimes reports another line; such a message,
//     "<file>.moon:<line>: <message>", becomes "<file>.moon: <lua syntax
//     error>".
//
// Cases whose output differs for other reasons are skipped with the reason
// (runnerKnownDiffs, smokeSkips, smokeSkipsLuaJIT).

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const runAsMoon = "GO_MOONSCRIPT_RUN_MOON"

func TestMain(m *testing.M) {
	if os.Getenv(runAsMoon) == "1" {
		os.Exit(run(append([]string{"moon"}, os.Args[1:]...), os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

type result struct {
	stdout, stderr string
	exit           int
}

// dialect is a Lua the runner runs on: the value of MOON_LUA and the
// directory of the references.
type dialect struct {
	env, ref string
}

var (
	lua51  = dialect{"", "ref"}
	luajit = dialect{"luajit", "ref_luajit"}
)

// runMoon runs the test binary as moon with args in dir.
func runMoon(t *testing.T, dir string, args ...string) result {
	t.Helper()
	return runMoonIn(t, lua51, dir, args...)
}

// runMoonIn runs the test binary as moon on d with args in dir.
func runMoonIn(t *testing.T, d dialect, dir string, args ...string) result {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), runAsMoon+"=1", "LUA_PATH=", luaEnv+"="+d.env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{stdout.String(), stderr.String(), code}
}

func readRef(t *testing.T, d dialect, dir, name string) result {
	t.Helper()
	read := func(ext string) string {
		data, err := os.ReadFile(filepath.Join(dir, d.ref, name+"."+ext))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	code, err := strconv.Atoi(strings.TrimSpace(read("exit")))
	if err != nil {
		t.Fatal(err)
	}
	return result{read("stdout"), read("stderr"), code}
}

var (
	refInstallDir = "/usr/local/share/lua/5.1/"
	noFileRe      = regexp.MustCompile(`(?m)^\tno file '[^'\n]*'\n`)
	luarocksRe    = regexp.MustCompile(`No LuaRocks module found for \S+`)
	luaSyntaxRe   = regexp.MustCompile(`(?m)^([^\s:]+\.moon):\d+: .*$`)
	addrRe        = regexp.MustCompile(`0x[0-9a-f]+`)
	errorsLuaRe   = regexp.MustCompile(`moonscript/errors\.lua:\d+`)
	driverFrameRe = regexp.MustCompile(`^\t(\S*/bin/moon|moon):\d+: `)
)

// normalize applies the normalizations described at the top of the file;
// ref marks reference output, stderr standard error.
func normalize(s string, ref, stderr bool) string {
	if ref {
		s = strings.ReplaceAll(s, refInstallDir, "")
		for _, prog := range []string{"/usr/bin/lua5.1: ", "/usr/bin/luajit: "} {
			s = strings.ReplaceAll(s, "\n"+prog, "\nmoon: ")
			if strings.HasPrefix(s, prog) {
				s = "moon: " + strings.TrimPrefix(s, prog)
			}
		}
	}
	s = luarocksRe.ReplaceAllString(s, "")
	s = noFileRe.ReplaceAllString(s, "")
	if stderr {
		s = luaSyntaxRe.ReplaceAllString(s, "$1: <lua syntax error>")
	}
	s = addrRe.ReplaceAllString(s, "0xADDR")
	s = errorsLuaRe.ReplaceAllString(s, "moonscript/errors.lua:N")
	lines := strings.Split(s, "\n")
	out := lines[:0]
	inDriver := false
	for _, line := range lines {
		if inDriver {
			if strings.HasPrefix(line, "\t") {
				continue
			}
			inDriver = false
		}
		if driverFrameRe.MatchString(line) {
			out = append(out, "\t<moon driver>")
			inDriver = true
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func compareResults(t *testing.T, want, got result) {
	t.Helper()
	if want.exit != got.exit {
		t.Errorf("exit status %d, want %d", got.exit, want.exit)
	}
	for _, s := range []struct{ name, want, got string }{
		{"stdout", want.stdout, got.stdout},
		{"stderr", want.stderr, got.stderr},
	} {
		w, g := normalize(s.want, true, s.name == "stderr"), normalize(s.got, false, s.name == "stderr")
		if w != g {
			t.Errorf("%s differs (normalized):\n--- want\n%s\n--- got\n%s\n--- raw got\n%s", s.name, w, g, s.got)
		}
	}
}

const runnerDir = "../../testdata/moon_runner"

type runnerCase struct {
	name string
	args []string
}

func readCases(t *testing.T) []runnerCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runnerDir, "cases.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []runnerCase
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		cases = append(cases, runnerCase{fields[0], fields[1:]})
	}
	return cases
}

// runnerKnownDiffs are cases whose output necessarily differs, with the
// reason.
var runnerKnownDiffs = map[string]string{}

func TestMoonRunner(t *testing.T) { testRunner(t, lua51, runnerKnownDiffs) }

// Differences of the VM's LuaJIT dialect from LuaJIT 2.1 (not of the
// runner), which the LuaJIT cases below are skipped for.
const (
	// LuaJIT replaces the frame of a tail call: error "x" in `return
	// error "x"` (MoonScript's implicit return) gets the position of the
	// frame below, or none if that is a C function or the coroutine's
	// base, tracebacks have no "(tail call)" entries and name a
	// tail-called function after its caller's call. The VM keeps Lua
	// 5.1's tail call frames.
	jitTailCall = "VM: LuaJIT 2.1 tail calls replace the frame (error positions, tracebacks); the VM keeps Lua 5.1's"
	// LuaJIT names no variable for a value indexed by a non-constant key
	// ("arithmetic on a string value"); Lua 5.1 says "field '?'".
	jitFieldName = "VM: LuaJIT 2.1 names no variable for t[k] in error messages, the VM says field '?' as Lua 5.1"
	// LuaJIT's hooks are global, so a coroutine runs the coverage hook;
	// Lua 5.1's are per thread. LuaJIT's constant table constructors
	// (TDUP) have no instructions on the lines of the constant fields, so
	// these get no line events.
	jitHooks = "VM: LuaJIT 2.1 line hooks are global (coroutines too) and constant table fields have no line events; the VM's are Lua 5.1's"
)

// runnerKnownDiffsLuaJIT are the cases whose output necessarily differs
// from luajit's, with the reason.
var runnerKnownDiffsLuaJIT = map[string]string{
	"api":                   jitTailCall,
	"closures":              jitTailCall,
	"err_coroutine":         jitTailCall,
	"err_level2":            jitTailCall,
	"err_nested_pcall":      jitTailCall,
	"require":               jitTailCall,
	"require_runtime_error": jitTailCall,
	"err_arith":             jitFieldName,
	"coverage_loop":         jitHooks,
	"coverage_report":       jitHooks,
	"coverage_report_c":     jitHooks,
	"coverage_require":      jitHooks + "; " + jitTailCall,
}

func TestMoonRunnerLuaJIT(t *testing.T) { testRunner(t, luajit, runnerKnownDiffsLuaJIT) }

func testRunner(t *testing.T, d dialect, knownDiffs map[string]string) {
	cases := readCases(t)
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if reason, ok := knownDiffs[c.name]; ok {
				t.Skip("known difference: " + reason)
			}
			want := readRef(t, d, runnerDir, c.name)
			got := runMoonIn(t, d, runnerDir, c.args...)
			compareResults(t, want, got)
		})
	}
}

const smokeDir = "../../testdata"

// smokeSkips are upstream scripts whose output can't match, with the reason.
var smokeSkips = map[string]string{
	"docs_reference_097": "uses the lpeg global that the reference's lpeg C module sets",
}

// smokeSkipsLuaJIT are the upstream scripts whose output can't match
// luajit's, with the reason.
var smokeSkipsLuaJIT = map[string]string{
	"docs_reference_097":    smokeSkips["docs_reference_097"],
	"docs_reference_116":    jitTailCall,
	"docs_standard_lib_013": "VM: LuaJIT 2.1 reports a failing tail call whose arguments span lines at the line they end on, the VM at the line of the call as Lua 5.1",
	"spec_inputs_literals":  "VM: no FFI, so evaluating LuaJIT's 64-bit integer literals (1LL, 1ULL) is an error",
}

// TestMoonLuaEnv: MOON_LUA selects the dialect, an unknown value is an
// error.
func TestMoonLuaEnv(t *testing.T) {
	for _, c := range []struct {
		env, want string
	}{
		{"", "Lua 5.1\n"},
		{"lua51", "Lua 5.1\n"},
		{"luajit", "LuaJIT 2.1.0-beta3\n"},
	} {
		got := runMoonIn(t, dialect{c.env, ""}, runnerDir, "-e", `print jit and jit.version or _VERSION`)
		if got.exit != 0 || got.stdout != c.want || got.stderr != "" {
			t.Errorf("MOON_LUA=%s: %+v, want %q", c.env, got, c.want)
		}
	}
	got := runMoonIn(t, dialect{"lua5.3", ""}, runnerDir, "hello.moon")
	want := result{"", "moon: MOON_LUA: unknown Lua dialect \"lua5.3\" (lua51 or luajit)\n", 1}
	if got != want {
		t.Errorf("MOON_LUA=lua5.3: %+v, want %+v", got, want)
	}
}

func TestMoonSmoke(t *testing.T) { testSmoke(t, lua51, smokeSkips) }

func TestMoonSmokeLuaJIT(t *testing.T) { testSmoke(t, luajit, smokeSkipsLuaJIT) }

func testSmoke(t *testing.T, d dialect, skips map[string]string) {
	data, err := os.ReadFile(filepath.Join(smokeDir, "moon_smoke", "scripts.txt"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, script := range strings.Split(string(data), "\n") {
		if script == "" || strings.HasPrefix(script, "#") {
			continue
		}
		n++
		name := strings.TrimSuffix(strings.ReplaceAll(script, "/", "_"), ".moon")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := readRef(t, d, filepath.Join(smokeDir, "moon_smoke"), name)
			if want.exit == 124 {
				t.Skip("does not terminate (the reference run timed out)")
			}
			if reason, ok := skips[name]; ok {
				t.Skip(reason)
			}
			compareResults(t, want, runMoonIn(t, d, smokeDir, script))
		})
	}
	if n == 0 {
		t.Fatal("no scripts")
	}
}

// TestCoverageFlags: as in the reference, -v wins over -c, and -c without a
// script prints usage. moon -c runs are cases of TestMoonRunner
// (coverage*), with references from the reference moon running the port's
// fixed moonscript.cmd.coverage (upstream's prints an empty report on Lua
// 5.1, see main.go); cov_report.moon also drives the module with a hook of
// its own.
func TestCoverageFlags(t *testing.T) {
	if got := runMoon(t, runnerDir, "-cv"); got.exit != 0 || got.stdout != "MoonScript version 0.8.0\n" {
		t.Errorf("moon -cv: %+v", got)
	}
	if got := runMoon(t, runnerDir, "-c"); got.exit != 1 || !strings.HasPrefix(got.stderr, "Usage: moon [options] script [args]") {
		t.Errorf("moon -c: %+v", got)
	}
}
