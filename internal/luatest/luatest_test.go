package luatest_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/luatest"
)

func TestMain(m *testing.M) { luatest.Main(m) }

// recorder stands in for a test to observe whether an assertion fails.
type recorder struct{ messages []string }

func (r *recorder) Errorf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

// defaults are the default run options.
func defaults() luatest.RunOptions {
	return luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}
}

// named is an interpreter known only by its name, for results built by hand.
func named(name string) luatest.Interpreter {
	return luatest.Interpreter{Name: name, Path: "", Version: ""}
}

// result is a run of the interpreter named interp that printed stdout and
// stderr and exited with code.
func result(interp, stdout, stderr string, code int) luatest.Result {
	return luatest.Result{
		Interp: named(interp), Dir: "", Script: "", Args: nil,
		Stdout: stdout, Stderr: stderr, ExitCode: code,
	}
}

// TestDifferential_SeesMissingModule is the property the harness exists for,
// without any bundler in the way: a "bundle" that carries no module at all
// (the entry script itself) must not pass for the original, because
// RunBundle runs it where the module's file does not exist.
func TestDifferential_SeesMissingModule(t *testing.T) {
	t.Parallel()

	project := luatest.Project{
		"main.lua": "print((require(\"m\")))\n",
		"m.lua":    "return \"m\"\n",
	}

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		orig := luatest.Run(t, interp, luatest.WriteProject(t, project), defaults(), "main.lua")
		require.Equal(t, 0, orig.ExitCode, "the original must work\n%s", orig)

		bundle := luatest.RunBundle(t, interp, []byte(project["main.lua"]), defaults())
		rec := &recorder{messages: nil}
		assert.False(t, luatest.AssertSame(rec, orig, bundle), "AssertSame must fail\n%s", bundle)
		require.Len(t, rec.messages, 4, "exit status, stdout and message differ, then both runs")
		assert.Contains(t, rec.messages[0], "exit status differs")
		assert.Contains(t, rec.messages[1], "stdout differs")
		assert.Contains(t, rec.messages[2], "error message differs")
		assert.Contains(t, rec.messages[3], "=== bundle: "+interp.Name+" bundle.lua")
		assert.Equal(t, "module 'm' not found:", bundle.Message())
	})
}

func TestAssertSame_Passes(t *testing.T) {
	t.Parallel()

	orig := result("lua5.4", "hi\n",
		"lua5.4: ./m.lua:2: boom\nstack traceback:\n\t./m.lua:2: in main chunk\n", 1)
	bundle := result("lua5.4", "hi\n",
		"lua5.4: bundle.lua:17: boom\nstack traceback:\n\tbundle.lua:17: in function <bundle.lua:12>\n", 1)

	rec := &recorder{messages: nil}
	assert.True(t, luatest.AssertSame(rec, orig, bundle))
	assert.Empty(t, rec.messages)
}

func TestAssertSame_ReportsEachDifference(t *testing.T) {
	t.Parallel()

	base := result("lua", "a\n", "", 0)

	for name, tt := range map[string]struct {
		bundle luatest.Result
		want   string
	}{
		"exit status": {result("lua", "a\n", "", 3), "exit status differs"},
		"stdout":      {result("lua", "a\nb\n", "", 0), "stdout differs"},
		"no newline":  {result("lua", "a", "", 0), "stdout differs"},
		"stderr":      {result("lua", "a\n", "warning\n", 0), "error message differs"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := &recorder{messages: nil}
			assert.False(t, luatest.AssertSame(rec, base, tt.bundle))
			require.Len(t, rec.messages, 2, "the difference, then both runs")
			assert.Contains(t, rec.messages[0], tt.want)
			assert.Contains(t, rec.messages[1], "=== original: lua")

			// The single-aspect assertions carry both runs themselves.
			rec = &recorder{messages: nil}
			luatest.AssertSameExitCode(rec, base, tt.bundle)
			luatest.AssertSameStdout(rec, base, tt.bundle)
			luatest.AssertSameStderr(rec, base, tt.bundle)
			require.Len(t, rec.messages, 1)
			assert.Contains(t, rec.messages[0], tt.want)
			assert.Contains(t, rec.messages[0], "=== original: lua")
		})
	}
}

// tarantoolTrailer is what Tarantool 3.9 appends to an uncaught error.
const tarantoolTrailer = `{"type":"LuajitError","trace":[{"file":"src/lua/utils.c","line":737}]}`

// TestNormalizeStderr uses stderr captured from real runs: the original from the
// project directory, the bundle from an empty one.
func TestNormalizeStderr(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, interp, orig, bundle, want string
	}{
		{
			name:   "PUC Lua 5.5",
			interp: "lua",
			orig: "lua: ./boom.lua:2: attempt to index a nil value (field 'x')\n" +
				"stack traceback:\n\t./boom.lua:2: in main chunk\n\t[C]: in global 'require'\n" +
				"\tmain.lua:2: in main chunk\n\t[C]: in ?\n",
			bundle: "lua: bundle.lua:11: attempt to index a nil value (field 'x')\n" +
				"stack traceback:\n\tbundle.lua:11: in function <bundle.lua:6>\n" +
				"\t[C]: in global 'require'\n\tbundle.lua:33: in main chunk\n\t[C]: in ?\n",
			want: "attempt to index a nil value (field 'x')",
		},
		{
			name:   "LuaJIT",
			interp: "luajit",
			orig: "luajit: ./boom.lua:2: attempt to index field 'x' (a nil value)\n" +
				"stack traceback:\n\t./boom.lua:2: in main chunk\n" +
				"\t[C]: in function 'require'\n\t[C]: at 0x0102018c0c\n",
			bundle: "luajit: bundle.lua:11: attempt to index field 'x' (a nil value)\n" +
				"stack traceback:\n\tbundle.lua:11: in function <bundle.lua:6>\n" +
				"\t[C]: at 0x01005050e8\n",
			want: "attempt to index field 'x' (a nil value)",
		},
		{
			name:   "Tarantool",
			interp: "tarantool",
			orig: "...0_yzc03j72jfczdln8mqwnl40000gp/T/tmp.t1CS1M3jYS/boom.lua:2: " +
				"attempt to index field 'x' (a nil value) " + tarantoolTrailer + "\n" +
				"fatal error, exiting the event loop\n",
			bundle: "bundle.lua:11: attempt to index field 'x' (a nil value) " +
				tarantoolTrailer + "\nfatal error, exiting the event loop\n",
			want: "attempt to index field 'x' (a nil value)",
		},
		{
			name:   "module not found, Lua 5.1",
			interp: "lua5.1",
			orig: "lua5.1: main.lua:1: module 'nothere' not found:\n" +
				"\tno field package.preload['nothere']\n\tno file './nothere.lua'\n" +
				"\tno file './nothere/init.lua'\nstack traceback:\n\t[C]: ?\n",
			bundle: "lua5.1: bundle.lua:9: module 'nothere' not found:\n" +
				"\tno field package.preload['nothere']\n\tno field package.postload['nothere']\n" +
				"\tno file './nothere.lua'\nstack traceback:\n\t[C]: ?\n",
			want: "module 'nothere' not found:",
		},
		{
			// Tarantool lists absolute paths up to / and cuts a long message
			// short, trailer included.
			name:   "module not found, Tarantool",
			interp: "tarantool",
			orig: "main.lua:1: module 'nothere' not found:\n" +
				"\tno field package.preload['nothere']\n" +
				"\tno file '/private/tmp/proj/override/nothere.lua'\n" +
				"\tno file '/usr/fatal error, exiting the event loop\n",
			bundle: "bundle.lua:9: module 'nothere' not found:\n" +
				"\tno field package.preload['nothere']\n" +
				"\tno file '/private/tmp/empty/override/nothere.lua'\n" +
				"\tno file '/usr/share/tarantool/nothere.lua'\n" +
				"\tno file '/usr/lib/fatal error, exiting the event loop\n",
			want: "module 'nothere' not found:",
		},
		{
			name:   "stacked positions",
			interp: "lua5.4",
			orig:   "lua5.4: rethrow.lua:3: rethrow.lua:1: deep\nstack traceback:\n",
			bundle: "lua5.4: bundle.lua:40: bundle.lua:38: deep\nstack traceback:\n",
			want:   "deep",
		},
		{
			name:   "debug chunk name shortened to LUA_IDSIZE",
			interp: "lua",
			orig:   "lua: ./boom.lua:3: kaboom\nstack traceback:\n",
			bundle: "lua: ...T/TestDebugTraceback123/001/boom.lua:3: kaboom\nstack traceback:\n",
			want:   "kaboom",
		},
		{
			name:   "string chunk",
			interp: "luajit",
			orig:   "luajit: [string \"local t = {} t.x.y = 1\"]:1: attempt to index a nil value\n",
			bundle: "luajit: [string \"local t = {} t.x.y = 1\"]:1: attempt to index a nil value\n",
			want:   "attempt to index a nil value",
		},
		{
			name:   "table error object, Tarantool",
			interp: "tarantool",
			orig:   "table: 0x0103f429b0 " + tarantoolTrailer + "\nfatal error, exiting the event loop\n",
			bundle: "table: 0x0104a0c1f8 " + tarantoolTrailer + "\nfatal error, exiting the event loop\n",
			want:   "table: 0x?",
		},
		{
			name:   "program output on stderr",
			interp: "lua",
			orig:   "warn line\n",
			bundle: "warn line\n",
			want:   "warn line",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, luatest.NormalizeStderr(tt.interp, tt.orig), "original")
			assert.Equal(t, tt.want, luatest.NormalizeStderr(tt.interp, tt.bundle), "bundle")
		})
	}
}

// TestNormalizeStderr_KeepsDifferences guards against normalising too much:
// what tells a working bundle from a broken one must survive.
func TestNormalizeStderr_KeepsDifferences(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, interp, a, b string }{
		{"message", "lua",
			"lua: ./boom.lua:3: kaboom\n",
			"lua: bundle.lua:9: module 'boom' not found:\n"},
		{"module name", "lua5.1",
			"lua5.1: main.lua:1: module 'a' not found:\n",
			"lua5.1: bundle.lua:1: module 'b' not found:\n"},
		{"empty versus error", "luajit",
			"",
			"luajit: bundle.lua:14: '<eof>' expected near 'print'\n"},
		{"extra program output", "lua",
			"one\n",
			"one\ntwo\n"},
		{"stray tab-only line", "lua",
			"lua: m.lua:1: module 'x' not found:\n\tno file './x.lua'\n",
			"lua: bundle.lua:1: module 'x' not found:\n\tno file './x.lua'\n\t\n" +
				"\tno field package.postload['x']\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.NotEqual(t, luatest.NormalizeStderr(tt.interp, tt.a), luatest.NormalizeStderr(tt.interp, tt.b))
		})
	}
}

func TestStripPositions(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"first: \tfalse\t./flaky.lua:1: init failed":     "first: \tfalse\tinit failed",
		"first: \tfalse\tbundle.lua:10: init failed":     "first: \tfalse\tinit failed",
		"./a.lua:3: ./b.lua:1: boom":                     "boom",
		"...ath/to/flaky.lua:1: x\n...ath/to/b.lua:2: y": "x\ny",
		"no position here: 12 apples":                    "no position here: 12 apples",
		"[C]: in function 'error'":                       "[C]: in function 'error'",
	} {
		assert.Equal(t, want, luatest.StripPositions(input), "StripPositions(%q)", input)
	}
}

func TestRun_ReportsExitStatusAndOutput(t *testing.T) {
	t.Parallel()

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		dir := luatest.WriteProject(t, luatest.Project{
			"exit3.lua": "io.write(\"out\") io.stderr:write(\"err\\n\") os.exit(3)\n",
			"raise.lua": "error(\"boom\")\n",
		})

		r := luatest.Run(t, interp, dir, defaults(), "exit3.lua")
		assert.Equal(t, 3, r.ExitCode, "%s", r)
		assert.Equal(t, "out", r.Stdout)
		assert.Equal(t, "err\n", r.Stderr)

		r = luatest.Run(t, interp, dir, defaults(), "raise.lua")
		assert.Equal(t, 1, r.ExitCode, "%s", r)
		assert.Equal(t, "boom", r.Message(), "%s", r)
	})
}

func TestRun_PassesArguments(t *testing.T) {
	t.Parallel()

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		dir := luatest.WriteProject(t, luatest.Project{"args.lua": "print(select('#', ...), ...)\n"})

		r := luatest.Run(t, interp, dir, defaults(), "args.lua", "x", "y z")
		assert.Equal(t, "2\tx\ty z\n", r.Stdout, "%s", r)
	})
}

// TestRun_ScrubsEnvironment checks that nothing from the test process reaches
// the interpreter but PATH, HOME, LUA_PATH and what the options add.
func TestRun_ScrubsEnvironment(t *testing.T) {
	// t.Setenv forbids t.Parallel().
	t.Setenv("LUAMERGE_LEAK", "leaked")
	t.Setenv("LUA_INIT", "print('LUA_INIT ran')")

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		dir := luatest.WriteProject(t, luatest.Project{
			"env.lua": "print(os.getenv('LUAMERGE_LEAK'), os.getenv('LUA_PATH'), os.getenv('EXTRA'))\n",
		})

		r := luatest.Run(t, interp, dir, luatest.RunOptions{LuaPath: "", Env: []string{"EXTRA=x"}, Timeout: 0},
			"env.lua")
		require.Equal(t, 0, r.ExitCode, "%s", r)
		assert.Equal(t, "nil\t"+luatest.DefaultLuaPath+"\tx\n", r.Stdout)

		r = luatest.Run(t, interp, dir, luatest.RunOptions{LuaPath: "./?.lua", Env: nil, Timeout: 0}, "env.lua")
		assert.Equal(t, "nil\t./?.lua\tnil\n", r.Stdout, "%s", r)
	})
}

// TestDefaultLuaPath_FindsInitModules is why DefaultLuaPath exists: without it
// Lua 5.1, 5.2 and LuaJIT cannot load foo/init.lua natively.
func TestDefaultLuaPath_FindsInitModules(t *testing.T) {
	t.Parallel()

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		dir := luatest.WriteProject(t, luatest.Project{
			"main.lua":     "print((require(\"foo\")))\n",
			"foo/init.lua": "return \"foo/init.lua\"\n",
		})

		r := luatest.Run(t, interp, dir, defaults(), "main.lua")
		assert.Equal(t, "foo/init.lua\n", r.Stdout, "%s", r)
	})
}

func TestRun_TimesOut(t *testing.T) {
	t.Parallel()

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		dir := luatest.WriteProject(t, luatest.Project{"loop.lua": "while true do end\n"})
		opts := luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 300 * time.Millisecond}
		start := time.Now()

		_, err := luatest.RunForTest(t.Context(), interp, dir, opts, "loop.lua", nil)
		require.ErrorContains(t, err, "did not finish within 300ms")
		assert.Less(t, time.Since(start), 5*time.Second)
	})
}

// TestRun_DoesNotWaitForLeftoverProcesses checks that a process the script
// leaves behind, holding the script's stdout and stderr open, neither fails
// the run nor holds it up: the run is over when the interpreter exits.
func TestRun_DoesNotWaitForLeftoverProcesses(t *testing.T) {
	t.Parallel()

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		dir := luatest.WriteProject(t, luatest.Project{"leave.lua": "os.execute('sleep 10 &')\nprint('done')\n"})
		start := time.Now()

		r := luatest.Run(t, interp, dir, defaults(), "leave.lua")
		assert.Equal(t, 0, r.ExitCode, "%s", r)
		assert.Equal(t, "done\n", r.Stdout, "%s", r)
		assert.Less(t, time.Since(start), 5*time.Second, "the run waited for the process left behind")
	})
}

func TestRunBundle_UsesAnEmptyDirectory(t *testing.T) {
	t.Parallel()

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		script := "for _, f in ipairs({'bundle.lua', 'main.lua', 'm.lua'}) do\n" +
			"  local h = io.open(f)\n  print(f, h ~= nil)\n  local _ = h and h:close()\nend\n"

		r := luatest.RunBundle(t, interp, []byte(script), defaults())
		require.Equal(t, 0, r.ExitCode, "%s", r)
		assert.Equal(t, "bundle.lua\ttrue\nmain.lua\tfalse\nm.lua\tfalse\n", r.Stdout)

		entries, err := os.ReadDir(r.Dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "bundle.lua", entries[0].Name())
	})
}

func TestWriteProject(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{"a/b/c.lua": "x", "d.lua": "y"})

	for rel, want := range map[string]string{"a/b/c.lua": "x", "d.lua": "y"} {
		data, err := fs.ReadFile(os.DirFS(dir), rel)
		require.NoError(t, err)
		assert.Equal(t, want, string(data))
	}
}

func TestProjectFile_RejectsPathsOutside(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"../x.lua", "a/../../x.lua", "/abs.lua", ""} {
		_, err := luatest.ProjectFileForTest("/p", bad)
		require.Error(t, err, "%q must be refused", bad)
	}

	path, err := luatest.ProjectFileForTest("/p", "a/b.lua")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/p", "a", "b.lua"), path)
}

func TestCopyDir(t *testing.T) {
	t.Parallel()

	src := luatest.WriteProject(t, luatest.Project{"main.lua": "m", "sub/x.lua": "x"})
	dst := luatest.CopyDir(t, src)
	assert.NotEqual(t, src, dst)

	for rel, want := range map[string]string{"main.lua": "m", "sub/x.lua": "x"} {
		data, err := fs.ReadFile(os.DirFS(dst), rel)
		require.NoError(t, err)
		assert.Equal(t, want, string(data))
	}
}

var (
	// errNotFound is what the fake PATH lookup answers for a missing name.
	errNotFound = errors.New("not found")
	// errDangling is what the fake link resolution answers for a link whose
	// target is gone.
	errDangling = errors.New("no such file or directory")
)

func TestDiscover(t *testing.T) {
	t.Parallel()

	// A fake PATH: lua is Ubuntu's alternatives link to lua5.4.
	onPath := map[string]string{
		"lua5.1": "/usr/bin/lua5.1",
		"lua5.4": "/usr/bin/lua5.4",
		"lua":    "/usr/bin/lua",
		"luajit": "/usr/bin/luajit",
	}
	lookPath := func(name string) (string, error) {
		path, ok := onPath[name]
		if !ok {
			return "", errNotFound
		}

		return path, nil
	}
	// The fake links; /usr/local/bin/lua5.3 is a link whose target is gone. It
	// is on the PATH of one subtest only.
	links := map[string]string{"/usr/bin/lua": "/usr/bin/lua5.4", "/usr/local/bin/lua5.3": ""}
	resolve := func(path string) (string, error) {
		target, ok := links[path]

		switch {
		case !ok:
			return path, nil
		case target == "":
			return "", errDangling
		default:
			return target, nil
		}
	}
	probe := func(interp luatest.Interpreter) (string, error) { return "version of " + interp.Name, nil }
	nothing := func(string) (string, error) { return "", errNotFound }
	names := func(found luatest.DiscoveryForTest) []string {
		out := make([]string, 0, len(found.Interps))
		for _, interp := range found.Interps {
			out = append(out, interp.Name)
		}

		return out
	}

	t.Run("dedupes by resolved path, keeping the versioned name", func(t *testing.T) {
		t.Parallel()

		found := luatest.DiscoverForTest("", false, lookPath, resolve, probe)
		assert.Empty(t, found.Problems)
		assert.Empty(t, found.Skip)
		assert.Equal(t, []string{"lua5.1", "lua5.4", "luajit"}, names(found))
		assert.Equal(t, "version of lua5.4", found.Interps[1].Version)
	})
	t.Run("nothing on PATH skips", func(t *testing.T) {
		t.Parallel()

		found := luatest.DiscoverForTest("", false, nothing, resolve, probe)
		assert.Empty(t, found.Problems)
		assert.Empty(t, found.Interps)
		assert.Contains(t, found.Skip, luatest.EnvInterpreters)
	})
	t.Run("nothing on PATH fails when interpreters are required", func(t *testing.T) {
		t.Parallel()

		found := luatest.DiscoverForTest("lua5.1,luajit", true, nothing, resolve, probe)
		assert.Empty(t, found.Skip)
		assert.Equal(t, []string{
			luatest.EnvInterpreters + " requires lua5.1, which is not on PATH",
			luatest.EnvInterpreters + " requires luajit, which is not on PATH",
		}, found.Problems)
	})
	t.Run("required and present", func(t *testing.T) {
		t.Parallel()

		found := luatest.DiscoverForTest(" lua5.1, lua ,luajit", true, lookPath, resolve, probe)
		assert.Empty(t, found.Problems)
		assert.Equal(t, []string{"lua5.1", "lua5.4", "luajit"}, names(found))
	})
	t.Run("one required interpreter missing", func(t *testing.T) {
		t.Parallel()

		found := luatest.DiscoverForTest("lua5.1,lua5.3", true, lookPath, resolve, probe)
		assert.Equal(t, []string{luatest.EnvInterpreters + " requires lua5.3, which is not on PATH"}, found.Problems)
	})
	t.Run("required name outside the candidates", func(t *testing.T) {
		t.Parallel()

		custom := func(name string) (string, error) {
			if name != "luajit-custom" {
				return "", errNotFound
			}

			return "/opt/bin/luajit-custom", nil
		}

		found := luatest.DiscoverForTest("luajit-custom", true, custom, resolve, probe)
		assert.Empty(t, found.Problems)
		assert.Equal(t, []string{"luajit-custom"}, names(found))
	})
	t.Run("set but empty is an error", func(t *testing.T) {
		t.Parallel()

		for _, value := range []string{"", " ", ",,"} {
			found := luatest.DiscoverForTest(value, true, lookPath, resolve, probe)
			require.Len(t, found.Problems, 1, "value %q", value)
			assert.Contains(t, found.Problems[0], "names no interpreter")
		}
	})
	t.Run("an interpreter that cannot run a script fails", func(t *testing.T) {
		t.Parallel()

		broken := func(interp luatest.Interpreter) (string, error) {
			if interp.Name == "luajit" {
				return "", errors.New("exit 127")
			}

			return "ok", nil
		}

		found := luatest.DiscoverForTest("", false, lookPath, resolve, broken)
		require.Len(t, found.Problems, 1)
		assert.True(t, strings.HasPrefix(found.Problems[0], "luajit (/usr/bin/luajit) cannot run"),
			found.Problems[0])
	})
	t.Run("a link that cannot be resolved fails", func(t *testing.T) {
		t.Parallel()

		withDangling := func(name string) (string, error) {
			if name == "lua5.3" {
				return "/usr/local/bin/lua5.3", nil
			}

			return lookPath(name)
		}

		found := luatest.DiscoverForTest("", false, withDangling, resolve, probe)
		assert.Equal(t, []string{"resolve lua5.3 (/usr/local/bin/lua5.3): " + errDangling.Error()}, found.Problems)
		assert.Equal(t, []string{"lua5.1", "lua5.4", "luajit"}, names(found))
	})
}

// TestRunCLI_ScrubsEnvironment checks that a LUAMERGE_* variable of the test
// process does not reach the CLI, and that the same variable does work when a
// test passes it, so the first half can fail.
func TestRunCLI_ScrubsEnvironment(t *testing.T) {
	dir := luatest.WriteProject(t, luatest.Project{"main.lua": "require(\"missing\")\n"})
	// t.Setenv forbids t.Parallel().
	t.Setenv("LUAMERGE_STRICT", "true")

	r := luatest.RunCLI(t, dir, nil, "--entry", "main.lua")
	assert.Equal(t, 0, r.ExitCode, "LUAMERGE_STRICT leaked into the CLI\n%s", r.Stderr)
	assert.Contains(t, r.Stdout, "package.preload[\"main\"]")

	r = luatest.RunCLI(t, dir, []string{"LUAMERGE_STRICT=true"}, "--entry", "main.lua")
	assert.NotEqual(t, 0, r.ExitCode, "LUAMERGE_STRICT passed explicitly must make the build strict")
	assert.Contains(t, r.Stderr, "unresolved require \"missing\"")
}

// TestDifferential_WithCLI runs the whole pipeline: project, CLI bundle, then
// the original and the bundle, with arguments, on every interpreter.
func TestDifferential_WithCLI(t *testing.T) {
	t.Parallel()

	luatest.Differential(t, luatest.Case{
		Project: luatest.Project{
			// The arguments are read from the global arg: the entry's own ...
			// differs in a bundle, where the entry runs as a required module.
			"main.lua": "local m = require(\"m\")\nprint(m.hello(arg[1], arg[2]))\n",
			"m.lua":    "return { hello = function(a, b) return \"hi \" .. a .. \",\" .. b end }\n",
		},
		Dir:     "",
		Entry:   "",
		Bundle:  luatest.CLI("--entry", "main.lua", "--strict"),
		Args:    []string{"a", "b"},
		Options: defaults(),
		Check: func(t *testing.T, orig, bundle luatest.Result) {
			t.Helper()
			luatest.AssertSame(t, orig, bundle)
			assert.Equal(t, "hi a,b\n", orig.Stdout, "%s", orig)
		},
	})
}
