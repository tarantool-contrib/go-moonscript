//go:build unix

package lua

// go-moonscript patch: io.open creates files with the permissions fopen
// gives them (see moonscript.patch).

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestIoOpenPermissions(t *testing.T) {
	umask := syscall.Umask(022)
	syscall.Umask(umask)
	name := filepath.Join(t.TempDir(), "f")
	L := NewState()
	defer L.Close()
	L.SetGlobal("name", LString(name))
	if err := L.DoString(`assert(io.open(name, "w")):close()`); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := st.Mode().Perm(), os.FileMode(0666&^umask); got != want {
		t.Errorf("permissions %v, want %v", got, want)
	}
}
