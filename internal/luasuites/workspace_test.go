//go:build linux

package luasuites

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	lua "github.com/tarantool-contrib/go-moonscript/lua"
)

// archives are the suites in testdata/archives, see SOURCES.md there.
var archives = []string{"lua5.1-tests", "luajit-tests", "tarantool", "vshard", "crud"}

// workspace is where the archives are extracted and the interpreters run.
type workspace struct {
	dir   string // root: the archives' contents under <dir>/<archive>
	lua   string // the `lua` wrapper script running luaMain
	runs  string // private directories of the runs
	files map[string][]archiveFile
}

type archiveFile struct {
	name string // path in the archive
	src  []byte
}

var (
	wsOnce sync.Once
	ws     *workspace
	wsErr  error
)

// getWorkspace extracts the archives once per test binary, into a
// directory removed by TestMain.
func getWorkspace(t *testing.T) *workspace {
	t.Helper()
	wsOnce.Do(func() { ws, wsErr = newWorkspace() })
	if wsErr != nil {
		t.Fatal(wsErr)
	}
	return ws
}

func newWorkspace() (*workspace, error) {
	dir, err := os.MkdirTemp("", "luasuites")
	if err != nil {
		return nil, err
	}
	w := &workspace{dir: dir, runs: filepath.Join(dir, "runs"), files: map[string][]archiveFile{}}
	for _, a := range archives {
		files, err := readArchive(filepath.Join("testdata", "archives", a+".tar.gz"))
		if err != nil {
			return nil, err
		}
		w.files[a] = files
		for _, f := range files {
			p := filepath.Join(dir, a, f.name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(p, f.src, 0o644); err != nil {
				return nil, err
			}
		}
	}
	// lua5.1-tests expects an empty libs/P1 (tar keeps it, readArchive
	// keeps only files)
	if err := os.MkdirAll(filepath.Join(dir, "lua5.1-tests", "lua5.1-tests", "libs", "P1"), 0o755); err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	w.lua = filepath.Join(dir, "bin", "lua")
	if err := os.MkdirAll(filepath.Dir(w.lua), 0o755); err != nil {
		return nil, err
	}
	script := fmt.Sprintf("#!/bin/sh\n%s=\"$0\" GOMAXPROCS=2 exec '%s' \"$@\"\n", childEnv, exe)
	if err := os.WriteFile(w.lua, []byte(script), 0o755); err != nil {
		return nil, err
	}
	return w, os.MkdirAll(w.runs, 0o755)
}

func readArchive(path string) ([]archiveFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	var files []archiveFile
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		src, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		files = append(files, archiveFile{name: hdr.Name, src: src})
	}
}

// privateDir returns a new directory for a run with symbolic links to the
// entries of dir, so that the files a test writes stay its own.
func (w *workspace) privateDir(dir, name string) (string, error) {
	p, err := os.MkdirTemp(w.runs, strings.NewReplacer("/", "_", ".", "_").Replace(name)+".")
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if err := os.Symlink(filepath.Join(dir, e.Name()), filepath.Join(p, e.Name())); err != nil {
			return "", err
		}
	}
	return p, nil
}

// result is what an interpreter run printed and how it ended.
type result struct {
	stdout   []byte
	stderr   []byte
	exit     int  // exit status, -1 when killed by a signal
	timedOut bool // killed after the timeout
}

// ended describes how a run ended that did not exit with status 0: a
// timeout, a crash of the Go runtime (stack overflow, out of memory, a
// panic: the reason, before the goroutine dump), or the exit status and
// the first line of the error report.
func (r *result) ended() string {
	if r.timedOut {
		return "timeout"
	}
	for _, crash := range []string{"fatal error: ", "panic: "} {
		if i := bytes.Index(r.stderr, []byte(crash)); i >= 0 && bytes.Contains(r.stderr[i:], []byte("\ngoroutine ")) {
			return "crash: " + firstLine(r.stderr[i:])
		}
	}
	if r.exit < 0 {
		return "crash: " + lastLine(r.stderr)
	}
	return fmt.Sprintf("exit %d: %s", r.exit, firstLine(r.stderr))
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(string(b), "\n")
	return s
}

func lastLine(b []byte) string {
	s := strings.TrimRight(string(b), "\n")
	return s[strings.LastIndexByte(s, '\n')+1:]
}

// slots limits the interpreter processes running at once.
var slots = make(chan struct{}, runtime.NumCPU())

// timeout is the time limit of an interpreter run (slowUnits have theirs).
const timeout = 120 * time.Second

// run runs the interpreter in dir, with a time limit and the given LUA_PATH
// and arguments, as PUC Lua 5.1.
func (w *workspace) run(dir string, limit time.Duration, luaPath string, args ...string) *result {
	return w.runDialect(lua.DialectLua51, dir, limit, luaPath, args...)
}

// runDialect is run with the interpreter in the given dialect.
func (w *workspace) runDialect(dialect lua.Dialect, dir string, limit time.Duration, luaPath string, args ...string) *result {
	slots <- struct{}{}
	defer func() { <-slots }()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.lua, args...)
	cmd.Dir = dir
	cmd.Env = append(filteredEnv(), "LUA_PATH="+luaPath)
	if dialect == lua.DialectLuaJIT {
		cmd.Env = append(cmd.Env, dialectEnv+"=luajit")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := &result{stdout: stdout.Bytes(), stderr: stderr.Bytes(), timedOut: ctx.Err() != nil}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		r.exit = ee.ExitCode()
	default:
		r.exit = -1
		r.stderr = append(r.stderr, err.Error()...)
	}
	return r
}

// filteredEnv is the environment of the interpreters: the test's, without
// what would change how Lua starts.
func filteredEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		switch name {
		case "LUA_INIT", "LUA_PATH", "LUA_CPATH", childEnv, dialectEnv, "GOMAXPROCS":
			continue
		}
		env = append(env, e)
	}
	return env
}

// keepLines returns the lines of out that match re.
func keepLines(out []byte, re *regexp.Regexp) []string {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<24)
	for sc.Scan() {
		if re.MatchString(sc.Text()) {
			lines = append(lines, sc.Text())
		}
	}
	return lines
}

var (
	reAddress = regexp.MustCompile(`0x[0-9a-fA-F]{9,}`) // not a short hex numeral
	// the drivers in testdata/lua (/suites/lua in the reference runs, a
	// "..."-shortened path in a short_src), and the private directories
	reDriver = regexp.MustCompile(`(\.\.\.)?[^\s:"']*/((run51|ljrun|parse)\.lua)`)
	reRunDir = regexp.MustCompile(`(/tmp/tmp\.[0-9A-Za-z]+|runs/[^/\s]+)/`)
	reSpace  = regexp.MustCompile(`[\t\r\n]+`)
)

// maxMessage is the length messages are cut to in the ledger.
const maxMessage = 160

// normalize makes a message stable and fit for a TSV field: addresses
// become 0x?, the workspace's path disappears, tabs and newlines become
// spaces, and it is cut to maxMessage bytes.
func normalize(s string) string {
	if ws != nil {
		s = strings.ReplaceAll(s, ws.dir+"/", "")
	}
	s = reDriver.ReplaceAllString(s, "$2")
	s = reRunDir.ReplaceAllString(s, "")
	s = reAddress.ReplaceAllString(s, "0x?")
	s = reSpace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if len(s) > maxMessage {
		s = s[:maxMessage] + "..."
	}
	return strings.ToValidUTF8(s, "?")
}
