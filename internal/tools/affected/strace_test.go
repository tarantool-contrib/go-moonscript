package main

import (
	"strings"
	"testing"
)

func TestParseStrace(t *testing.T) {
	const trace = `100 execve("./x.test", ["./x.test"], 0x7ffd /* 5 vars */) = 0
100 openat(AT_FDCWD</r/lua/parse>, "../testdata/compat", O_RDONLY|O_CLOEXEC) = 3</r/lua/testdata/compat>
100 newfstatat(AT_FDCWD</r/lua/parse>, "/etc/passwd", {st_mode=S_IFREG|0644, ...}, 0) = 0
101 openat(3</r/lua/testdata/compat>, "a b\"c\\d\x41.lua", O_RDONLY) = 4</r/lua/testdata/compat/a b"c\dA.lua>
101 newfstatat(4</r/lua/testdata/compat/a.lua>, "", {st_mode=S_IFREG|0644, ...}, AT_EMPTY_PATH) = 0
102 openat(AT_FDCWD</tmp/w>, "../../r/go.mod", O_RDONLY <unfinished ...>
100 --- SIGURG {si_signo=SIGURG, si_code=SI_TKILL, si_pid=100, si_uid=1000} ---
102 <... openat resumed>) = -1 ENOENT (No such file or directory)
103 stat("rel/x", 0xc000) = 0
100 clone(child_stack=NULL, flags=CLONE_VM|CLONE_VFORK|SIGCHLD <unfinished ...>
104 execve("/usr/bin/git", ["git", "status"], 0x7ffd /* 5 vars */ <unfinished ...>
104 <... execve resumed>)             = 0
100 <... clone resumed>) = 104
104 clone3({flags=CLONE_VM|CLONE_FS|CLONE_THREAD, ...}, 88) = 105
105 openat(AT_FDCWD</r>, "lua/x.go", O_RDONLY) = 3</r/lua/x.go>
100 +++ exited with 0 +++
`
	got, err := ParseStrace(strings.NewReader(trace), "/r/lua/parse")
	if err != nil {
		t.Fatal(err)
	}
	want := []Access{
		{"/r/lua/parse/x.test", true, ""},
		{"/r/lua/testdata/compat", true, ""},
		{"/etc/passwd", true, ""},
		{`/r/lua/testdata/compat/a b"c\dA.lua`, true, ""},
		{"/r/go.mod", false, ""},
		{"/r/lua/parse/rel/x", true, ""},
		{"/usr/bin/git", true, "/usr/bin/git"},
		{"/r/lua/x.go", true, "/usr/bin/git"},
	}
	if len(got) != len(want) {
		t.Fatalf("ParseStrace:\n got %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("access %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestClassify(t *testing.T) {
	g := fakeGraph(t)
	flow := g.Packages["lua/flow"]
	deps := g.TestDeps("lua/flow")
	for rel, want := range map[string]string{
		"lua/flow/testdata/x.lua":   readOwn,
		"lua":                       readOwn, // an ancestor
		".git/HEAD":                 readOwn,
		"go.mod":                    readWhole,
		"lua/testdata/compat/a.lua": readTable,
		"lua/parse/parse.go":        readImports,
		"lua/parse":                 readImports,
		"lua/parse/testdata/a.lua":  readMissing, // data of a dependency
		"moonscript/x.moon":         readMissing,
		"lua/flow/README.md":        readMissing, // documentation selects no test
	} {
		if got, _ := classify(g, flow, deps, rel, false); got != want {
			t.Errorf("classify(lua/flow, %s) = %s, want %s", rel, got, want)
		}
	}
}
