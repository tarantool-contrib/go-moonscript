package main

// Port of upstream v0.8.0 spec/cmd_spec.moon (moonscript.cmd.moonc path
// helpers and compile_file_text). The inotify watcher case is skipped: the
// port doesn't support --watch.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCmdSpecPathHelpers(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) string
		in   string
		want string
	}{
		{"normalize_dir", normalizeDir, "hello/world/", "hello/world/"},
		{"normalize_dir", normalizeDir, "hello/world//", "hello/world/"},
		{"normalize_dir", normalizeDir, "", "/"}, // "wrong" per upstream, kept for parity
		{"normalize_dir", normalizeDir, "hello", "hello/"},

		{"parse_dir", parseDir, "/hello/world/file", "/hello/world/"},
		{"parse_dir", parseDir, "/hello/world/", "/hello/world/"},
		{"parse_dir", parseDir, "world", ""},
		{"parse_dir", parseDir, "", ""},

		{"parse_file", parseFile, "/hello/world/file", "file"},
		{"parse_file", parseFile, "/hello/world/", ""},
		{"parse_file", parseFile, "world", "world"},
		{"parse_file", parseFile, "", ""},

		{"convert_path", convertPath, "test.moon", "test.lua"},
		{"convert_path", convertPath, "/hello/file.moon", "/hello/file.lua"},
		{"convert_path", convertPath, "/hello/world/file", "/hello/world/file.lua"},
	}
	for _, c := range cases {
		if got := c.fn(c.in); got != c.want {
			t.Errorf("%s(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// "calculate target": path_to_target(path, target_dir, base_dir), nil
// arguments are the has* = false cases
func TestCmdSpecPathToTarget(t *testing.T) {
	cases := []struct {
		path, targetDir, baseDir string
		hasTarget, hasBase       bool
		want                     string
	}{
		{"test.moon", "", "", false, false, "test.lua"},
		{"hello/world.moon", "", "", false, false, "hello/world.lua"},
		{"test.moon", "compiled", "", true, false, "compiled/test.lua"},

		{"/home/leafo/test.moon", "", "", false, false, "/home/leafo/test.lua"},
		{"/home/leafo/test.moon", "compiled", "", true, false, "compiled/test.lua"},
		{"/home/leafo/test.moon", "/compiled/", "", true, false, "/compiled/test.lua"},

		{"moonscript/hello.moon", "", "moonscript", false, true, "moonscript/hello.lua"},
		{"moonscript/hello.moon", "out", "moonscript", true, true, "out/moonscript/hello.lua"},

		{"moonscript/package/hello.moon", "out", "moonscript/", true, true, "out/moonscript/package/hello.lua"},

		{"/home/leafo/moonscript/package/hello.moon", "/out", "/home/leafo/moonscript", true, true,
			"/out/moonscript/package/hello.lua"},
	}
	for _, c := range cases {
		if got := pathToTarget(c.path, c.targetDir, c.baseDir, c.hasTarget, c.hasBase); got != c.want {
			t.Errorf("pathToTarget(%q, %q, %q, %v, %v) = %q, want %q",
				c.path, c.targetDir, c.baseDir, c.hasTarget, c.hasBase, got, c.want)
		}
	}
}

func TestCmdSpecCompileFileText(t *testing.T) {
	got, err := compileFileText("print'hello'", compileOpts{fname: "test.moon"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "return print('hello')"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// "stubbed lfs / should make directory": upstream checks that moonc.mkdir
// creates every missing parent in order. The port has no separate mkdir; its
// writeFile creates the missing directories of the target path.
func TestCmdSpecMakeDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "hello/world/directory/out.lua")
	if err := writeFile(target, "return 1"); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"hello", "hello/world", "hello/world/directory"} {
		if st, err := os.Stat(filepath.Join(root, dir)); err != nil || !st.IsDir() {
			t.Errorf("%s was not created as a directory (%v)", dir, err)
		}
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "return 1\n" {
		t.Errorf("target = %q, %v", got, err)
	}
}

func TestCmdSpecInotifyWatcherDirs(t *testing.T) {
	// InotifyWacher {{"hello.moon", "hello.lua"}, {"cool/no.moon", "cool/no.lua"}}
	// \get_dirs! == {"./", "cool/"}
	t.Skip("not supported yet: moonc --watch (moonscript.cmd.watchers)")
}
