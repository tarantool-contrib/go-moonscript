package lua

// go-moonscript patch: tests of io.popen(cmd, "w") (see CHANGES.md).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A process opened with io.popen(cmd, "w") gets the end of its input when
// the file is closed (else close never returned), and its output goes to
// the standard output, as with popen(3).
func TestPopenWriteClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	out := filepath.Join(t.TempDir(), "out")
	L := NewState()
	defer L.Close()
	L.SetGlobal("out", LString(out))
	done := make(chan error, 1)
	go func() {
		done <- L.DoString(`
			local f = assert(io.popen("cat > '" .. out .. "'", "w"))
			assert(f:write("hello ", 42))
			assert(f:close())
			local g = assert(io.popen("cat", "w"))
			g:close()
		`)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("io.popen(cmd, \"w\"):close() does not return")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello 42" {
		t.Errorf("got %q", data)
	}
}

// TestPopenStdout is run by TestPopenWriteStdout in a child process: the
// output of a process opened for writing is the standard output.
func TestPopenStdout(t *testing.T) {
	if os.Getenv("GOPHER_LUA_POPEN_STDOUT") != "1" {
		t.Skip("run by TestPopenWriteStdout")
	}
	L := NewState()
	defer L.Close()
	if err := L.DoString(`local f = io.popen("cat", "w"); f:write("from cat\n"); f:close()`); err != nil {
		t.Fatal(err)
	}
}

func TestPopenWriteStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPopenStdout$", "-test.v")
	cmd.Env = append(os.Environ(), "GOPHER_LUA_POPEN_STDOUT=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if !strings.Contains(string(output), "from cat\n") {
		t.Errorf("the output of the process is lost:\n%s", output)
	}
}
