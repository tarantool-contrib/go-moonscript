package lua

// go-moonscript patch: tests of os.exit (see CHANGES.md), run in a
// child process: os.exit(true/false) is LuaJIT 2.1's (DialectLuaJIT only).

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestOsExitChild(t *testing.T) {
	code := os.Getenv("GOPHER_LUA_OS_EXIT")
	if code == "" {
		t.Skip("run by TestOsExit")
	}
	opts := Options{}
	if os.Getenv("GOPHER_LUA_OS_EXIT_JIT") != "" {
		opts.Dialect = DialectLuaJIT
	}
	L := NewState(opts)
	defer L.Close()
	if err := L.DoString(code); err != nil {
		t.Fatal(err)
	}
}

func TestOsExit(t *testing.T) {
	for _, c := range []struct {
		code   string
		jit    bool // DialectLuaJIT
		status int
		output string
	}{
		{`os.exit()`, false, 0, ""},
		{`os.exit(3)`, false, 3, ""},
		{`os.exit(2.7)`, false, 2, ""},
		{`print(pcall(os.exit, true)) os.exit(4)`, false, 4, "number expected, got boolean"},
		{`os.exit(true)`, true, 0, ""},
		{`os.exit(false)`, true, 1, ""},
		{`os.exit(7)`, true, 7, ""},
		{`print(pcall(os.exit, "x")) os.exit(5)`, false, 5, "number expected, got string"},
		{`print(pcall(os.exit, {})) os.exit(6)`, true, 6, "number expected, got table"},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOsExitChild$")
		cmd.Env = append(os.Environ(), "GOPHER_LUA_OS_EXIT="+c.code)
		if c.jit {
			cmd.Env = append(cmd.Env, "GOPHER_LUA_OS_EXIT_JIT=1")
		}
		output, err := cmd.CombinedOutput()
		status := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			status = exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if status != c.status || !strings.Contains(string(output), c.output) {
			t.Errorf("%s: status %d, output:\n%s", c.code, status, output)
		}
	}
}
