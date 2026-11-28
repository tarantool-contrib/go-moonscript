// Package luatest is the differential test harness for go-luamerge: it runs a
// multi-file Lua project natively and its bundle from an empty directory, on
// every Lua interpreter found on PATH, and compares what the two did.
//
// A bundle proves nothing when it runs next to its sources: `./?.lua` on
// package.path finds every module on disk, so a module the bundler left out is
// silently loaded from the project and the test passes. Here the original runs
// from the project directory and the bundle from a separate directory that
// holds nothing but the bundle.
//
// A differential test bundles a project once and checks it on every
// interpreter, one subtest each:
//
//	luatest.Differential(t, luatest.Case{
//		Project: luatest.Project{
//			"main.lua": `print(require("m").hello())` + "\n",
//			"m.lua":    `return { hello = function() return "hi" end }` + "\n",
//		},
//		Bundle: luatest.CLI("--entry", "main.lua", "--strict"),
//	})
//
// (The repository's linter wants every field of a Case literal spelled out;
// the example leaves the zero ones away for brevity.)
//
// The default check is AssertSame: the same stdout, the same exit status and
// the same error message (see NormalizeStderr). Set Case.Check to assert
// something sharper; Result keeps the raw output. For scenarios that are not
// "original versus bundle" (a host script that dofile()s the bundle, two
// bundles in one process) use ForEachInterpreter with Run and RunBundle.
//
// Interpreters are looked up on PATH (see Interpreters). With none found the
// Lua-executing tests skip, unless EnvInterpreters lists interpreters that must
// be present, in which case a missing one fails the test. CI sets it.
//
// A package that bundles through the CLI (CLI, RunCLI, CLIPath) must hand its
// TestMain to Main, which removes the binary built for the test process:
//
//	func TestMain(m *testing.M) { luatest.Main(m) }
//
// This is test support code: only _test.go files may import it.
package luatest

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// fileMode is the mode of every file the harness writes.
	fileMode = 0o600
	// dirMode is the mode of every directory the harness creates.
	dirMode = 0o750
)

// errOutsideProject refuses a project path that would leave the project.
var errOutsideProject = errors.New("not a relative path inside the project")

// Project is a Lua project: file contents by slash-separated path relative to
// the project directory.
type Project map[string]string

// WriteProject writes project into a fresh temporary directory and returns it.
func WriteProject(tb testing.TB, project Project) string {
	tb.Helper()

	dir := tb.TempDir()

	for rel, content := range project {
		path, err := projectFile(dir, rel)
		require.NoError(tb, err)
		require.NoError(tb, os.MkdirAll(filepath.Dir(path), dirMode))
		require.NoError(tb, os.WriteFile(path, []byte(content), fileMode))
	}

	return dir
}

// projectFile is the path of the project file rel inside dir. A path that
// would leave the project is refused.
func projectFile(dir, rel string) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("luatest: project path %q: %w", rel, errOutsideProject)
	}

	return filepath.Join(dir, filepath.FromSlash(rel)), nil
}

// CopyDir copies the directory tree src (e.g. a testdata fixture) into a fresh
// temporary directory and returns it, so neither a bundler nor the program can
// write into src. It is os.CopyFS: directories and regular files are copied,
// a symbolic link is recreated as a link.
func CopyDir(tb testing.TB, src string) string {
	tb.Helper()

	dst := tb.TempDir()
	require.NoError(tb, os.CopyFS(dst, os.DirFS(src)), "copy %s", src)

	return dst
}

// Bundler produces the bundle for the project in dir. It runs once per Case,
// before any interpreter; see CLI for the command-line tool, or write a
// closure around luamerge.Bundle.
type Bundler func(tb testing.TB, dir string) []byte

// Case is one differential test.
type Case struct {
	// Project is written to a fresh temporary directory. Set exactly one of
	// Project and Dir.
	Project Project
	// Dir is an existing project directory, copied to a fresh temporary
	// directory first (see CopyDir).
	Dir string
	// Entry is the script the original run executes, relative to the project
	// directory; "" means main.lua.
	Entry string
	// Bundle produces the bundle from the project directory.
	Bundle Bundler
	// Args are passed to both the original and the bundle.
	Args []string
	// Options apply to both runs.
	Options RunOptions
	// Check compares the runs; nil means AssertSame.
	Check func(t *testing.T, orig, bundle Result)
}

// Differential sets up the project of spec, bundles it once, and then, in one
// subtest per interpreter, runs `<interpreter> <entry> args...` from the
// project directory and `<interpreter> bundle.lua args...` from an empty
// directory and hands both results to spec.Check.
func Differential(t *testing.T, spec Case) {
	t.Helper()

	interps := Interpreters(t)

	if spec.Bundle == nil {
		t.Fatal("luatest: Case.Bundle is nil")
	}

	var dir string

	switch {
	case spec.Dir != "" && spec.Project != nil:
		t.Fatal("luatest: Case sets both Project and Dir")
	case spec.Dir != "":
		dir = CopyDir(t, spec.Dir)
	case spec.Project != nil:
		dir = WriteProject(t, spec.Project)
	default:
		t.Fatal("luatest: Case sets neither Project nor Dir")
	}

	entry := cmp.Or(spec.Entry, "main.lua")

	bundle := spec.Bundle(t, dir)
	if len(bundle) == 0 {
		// An empty chunk runs, prints nothing and exits 0: against an original
		// that does the same it would pass whatever the bundler did.
		t.Fatal("luatest: the bundler produced an empty bundle")
	}

	check := spec.Check
	if check == nil {
		check = func(t *testing.T, orig, bundle Result) {
			t.Helper()
			AssertSame(t, orig, bundle)
		}
	}

	for _, interp := range interps {
		t.Run(interp.Name, func(t *testing.T) {
			// A failure is reported at the test's call of Differential; the
			// exact line is in testify's error trace.
			t.Helper()

			orig := Run(t, interp, dir, spec.Options, entry, spec.Args...)
			bun := RunBundle(t, interp, bundle, spec.Options, spec.Args...)
			check(t, orig, bun)
		})
	}
}

// ForEachInterpreter runs subtest once per interpreter, as a subtest named
// after it. It skips or fails like Interpreters. The subtests run one after
// another, so subtest may collect results for the caller to compare
// afterwards.
//
// The linter treats subtest as a test helper, so it starts with t.Helper(); a
// failure in it is then reported at the test's call of ForEachInterpreter,
// and the exact line is in testify's error trace.
func ForEachInterpreter(t *testing.T, subtest func(t *testing.T, interp Interpreter)) {
	t.Helper()

	for _, interp := range Interpreters(t) {
		t.Run(interp.Name, func(t *testing.T) {
			t.Helper()
			subtest(t, interp)
		})
	}
}
