package resolve_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/luatest"
	"github.com/tarantool/go-luamerge/internal/resolve"
)

// searchpathProbe prints, for every name in the file named by its second
// argument (one per line), what package.searchpath(name, path) returns for the
// path given as its first argument: "+" and the file, or "-" when there is
// none. PUC Lua 5.1 has no package.searchpath; the probe then exits with
// probeNoSearchpath.
const searchpathProbe = `local path, names = ...
if not package.searchpath then
  io.write("no package.searchpath\n")
  os.exit(3)
end
for name in io.lines(names) do
  local found = package.searchpath(name, path)
  io.write(found and ("+" .. found) or "-", "\n")
end
`

// probeNoSearchpath is the exit status of searchpathProbe on an interpreter
// without package.searchpath.
const probeNoSearchpath = 3

// absTemplatePlaceholder in an oracleLayout's templates is replaced with the
// layout's own on-disk root once writeOracleLayout has created it, so a
// layout can name an absolute template without knowing the temp directory
// ahead of time.
const absTemplatePlaceholder = "{root}"

// oracleLayout is a project the oracle resolves every name in.
type oracleLayout struct {
	// name identifies the layout in failure messages.
	name string
	// files are slash paths relative to a base directory that holds the root,
	// so a file may lie outside the root. Each is a one-line module.
	files []string
	// dirs are directories named like module files.
	dirs []string
	// unreadable are files created without read permission.
	unreadable []string
	// root is the project root, relative to the base directory.
	root string
	// search are the search directories, relative to the root.
	search []string
	// templates is the path template list, as --path takes it.
	templates string
}

// oracleLayouts are the projects of the oracle. The root is called proj and
// the search directories lib and vendor; no name of oracleNames starts with
// "proj." and the ones that start with "lib." or "vendor." exist below the
// root, so none of the name extensions of the resolver (which apply only when
// the native lookup fails) can take part.
func oracleLayouts() []oracleLayout {
	return []oracleLayout{
		{
			name: "nested",
			files: []string{
				"foo.lua", "outside.lua", // Outside the root.
				"proj/init.lua", "proj/util.lua", "proj/sub/util.lua", "proj/sub/x.lua", "proj/sub/foo.lua",
				"proj/sub/sub/d.lua", "proj/foo.lua", "proj/.foo.lua", "proj/a/b/c.lua", "proj/a/b.c.lua",
				"proj/x/init.lua", "proj/m.lua", "proj/m/init.lua", "proj/t/init.lua", "proj/hidden/m.lua",
				"proj/.hidden.m.lua", "proj/foo-bar.lua",
			},
			dirs:       []string{"proj/weird.lua"},
			unreadable: []string{"proj/noread.lua"},
			root:       "proj",
			search:     nil,
			templates:  "?.lua;?/init.lua",
		},
		{
			name: "search-dirs",
			files: []string{
				"proj/dup.lua", "proj/lib/dup.lua", "proj/vendor/dup.lua", "proj/lib/only.lua",
				"proj/vendor/only.lua", "proj/vendor/v.lua", "proj/lib/p/q.lua", "proj/pkg.lua",
				"proj/lib/pkg/init.lua", "proj/pkg2/init.lua", "proj/lib/pkg2.lua", "proj/lib/util.lua",
				"proj/sub/util.lua", "proj/lib/foo.lua", "proj/vendor/sub/x.lua",
			},
			dirs:       nil,
			unreadable: nil,
			root:       "proj",
			search:     []string{"lib", "vendor"},
			templates:  "?.lua;?/init.lua",
		},
		{
			name: "templates",
			files: []string{
				"proj/k/k.lua", "proj/src/k.lua", "proj/src/s.lua", "proj/src/deep/e.lua", "proj/i/init.lua",
				"proj/src/util.lua", "proj/sub/src/util.lua", "proj/sub/sub.lua",
			},
			dirs:       nil,
			unreadable: nil,
			root:       "proj",
			search:     nil,
			templates:  "?.lua;?/?.lua;src/?.lua;?/init.lua",
		},
		{
			// An absolute template (its "?" spelled as {root}/abslib/?.lua,
			// an absolute path once expanded) names a file the relative
			// templates cannot reach, and must resolve exactly once even
			// though it is tried against the root and a search directory
			// both: LUA_PATH's own semantics never repeat an absolute entry
			// per search location, because a location is not what it is
			// relative to. The search directory is named "aux", not "lib" or
			// "vendor", so it stays clear of the "lib."/"vendor." names in
			// oracleNames that probe the resolver's own prefix-stripping
			// extension (a deliberate divergence from package.searchpath
			// that this oracle does not otherwise exercise, see oracleNames).
			name: "absolute-template",
			files: []string{
				"proj/local.lua", "proj/aux/shared.lua", "proj/abslib/only.lua",
			},
			dirs:       nil,
			unreadable: nil,
			root:       "proj",
			search:     []string{"aux"},
			templates:  "?.lua;" + absTemplatePlaceholder + "/abslib/?.lua;?/init.lua",
		},
	}
}

// oracleNames are the require names every layout is asked about: plain,
// dotted and path-style names, names with leading, trailing and doubled dots,
// and names that exist in no layout. A name with ";" is left out on purpose:
// Lua 5.4 splits the substituted path on it and Lua 5.2 does not.
func oracleNames() []string {
	return []string{
		"util", "sub.util", "sub.x", "x", "d", "sub.sub.d", "foo", "m", "t", "k", "s", "i", "init", "sub",
		"a.b.c", "a.b", "x.init", "m.init", "i.init", "deep.e", "k.k",
		"dup", "only", "v", "p.q", "pkg", "pkg2", "lib.util", "lib.only", "lib.p.q", "vendor.v", "lib.nope",
		"./foo", "../foo", ".foo", "./util", "../util", "../outside", "sub/util", "sub/x", "./sub/x", "a/b.c",
		"a/b/c", "./sub/../foo", ".hidden.m", "p/q", "../../foo",
		"util.", "sub.", "t.", "x.", "sub..util", ".util", "..util",
		"foo-bar", "weird", "noread", "missing", "sub.missing", "",
	}
}

// TestResolve_MatchesSearchpath is the oracle of the resolver: for every name
// of oracleNames in every layout of oracleLayouts, Resolve must return the
// file that the interpreter's own package.searchpath finds with the
// equivalent path, each template relative to the root and then relative to
// each search directory, in order; and no file when Lua finds none. The file
// that requires a name plays no part, in Lua as in Resolve.
func TestResolve_MatchesSearchpath(t *testing.T) {
	t.Parallel()

	names := oracleNames()

	for _, layout := range oracleLayouts() {
		t.Run(layout.name, func(t *testing.T) {
			t.Parallel()

			base := writeOracleLayout(t, layout)
			root := filepath.Join(base, layout.root)
			templates := strings.ReplaceAll(layout.templates, absTemplatePlaceholder, root)

			searchDirs := make([]string, 0, len(layout.search))
			for _, dir := range layout.search {
				searchDirs = append(searchDirs, filepath.Join(root, dir))
			}

			resolver := resolve.New(root, searchDirs, templates)
			path := luaPath(layout.search, templates)
			probe := writeProbe(t, names)

			luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
				t.Helper()

				lua := searchpath(t, interp, root, path, probe, len(names))

				var mismatches []string

				for i, name := range names {
					got := resolveFile(t, resolver, name)
					if got != lua[i] {
						mismatches = append(mismatches, fmt.Sprintf("  require(%q): Lua loads %s, Resolve returns %s",
							name, describeFile(base, lua[i]), describeFile(base, got)))
					}
				}

				if len(mismatches) > 0 {
					t.Errorf("layout %q (root %s, search %v, templates %q): %d of %d names resolve differently "+
						"from package.searchpath(name, %q) on %s:\n%s",
						layout.name, layout.root, layout.search, templates, len(mismatches), len(names), path,
						interp.Name, strings.Join(mismatches, "\n"))
				}
			})
		})
	}
}

// writeOracleLayout creates the files of layout in a fresh base directory and
// returns it.
func writeOracleLayout(t *testing.T, layout oracleLayout) string {
	t.Helper()

	base := t.TempDir()

	for _, rel := range append(append([]string(nil), layout.files...), layout.unreadable...) {
		path := filepath.Join(base, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf("return %q\n", rel)), 0o600))
	}

	for _, rel := range layout.dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(base, filepath.FromSlash(rel)), 0o750))
	}

	for _, rel := range layout.unreadable {
		require.NoError(t, os.Chmod(filepath.Join(base, filepath.FromSlash(rel)), 0))
	}

	require.NoError(t, os.MkdirAll(filepath.Join(base, layout.root), 0o750))

	return base
}

// luaPath is the package.path equivalent to a resolver searching search
// (relative to the root Lua runs in) with templates (already expanded, as
// --path takes them): every relative template relative to the root, then
// relative to each search directory, in order. An absolute template names
// the same file wherever it is tried, so — as it does for Resolve — it
// appears once, at the position it would take for the root, not once per
// location.
func luaPath(search []string, templates string) string {
	var path []string

	seenAbsolute := make(map[string]bool)

	for _, loc := range append([]string{"."}, search...) {
		for tmpl := range strings.SplitSeq(templates, ";") {
			if filepath.IsAbs(tmpl) {
				if seenAbsolute[tmpl] {
					continue
				}

				seenAbsolute[tmpl] = true
				path = append(path, tmpl)

				continue
			}

			path = append(path, loc+"/"+tmpl)
		}
	}

	return strings.Join(path, ";")
}

// writeProbe writes searchpathProbe and the names file into a fresh directory,
// outside every layout, and returns the probe's path.
func writeProbe(t *testing.T, names []string) string {
	t.Helper()

	dir := t.TempDir()
	probe := filepath.Join(dir, "searchpath.lua")
	require.NoError(t, os.WriteFile(probe, []byte(searchpathProbe), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "names.txt"), []byte(strings.Join(names, "\n")+"\n"), 0o600))

	return probe
}

// searchpath runs the probe with interp in root and returns, per name, the
// absolute file package.searchpath found, or "" for none. It skips the test on
// an interpreter without package.searchpath.
func searchpath(t *testing.T, interp luatest.Interpreter, root, path, probe string, count int) []string {
	t.Helper()

	opts := luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}
	names := filepath.Join(filepath.Dir(probe), "names.txt")

	result := luatest.Run(t, interp, root, opts, probe, path, names)
	if result.ExitCode == probeNoSearchpath {
		t.Skipf("%s has no package.searchpath", interp)
	}

	require.Equal(t, 0, result.ExitCode, "the probe failed\n%s", result)

	lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
	require.Len(t, lines, count, "the probe printed one line per name\n%s", result)

	files := make([]string, 0, count)

	for _, line := range lines {
		switch {
		case line == "-":
			files = append(files, "")
		case strings.HasPrefix(line, "+"):
			// package.searchpath prints the entry as found: relative to
			// root when the matching template was relative (interp ran with
			// root as its working directory), already absolute when the
			// matching template was an absolute path — joining root onto
			// that would double it, exactly the bug this oracle guards.
			found := strings.TrimPrefix(line, "+")
			if filepath.IsAbs(found) {
				files = append(files, filepath.Clean(found))
			} else {
				files = append(files, filepath.Join(root, found))
			}
		default:
			t.Fatalf("the probe printed %q\n%s", line, result)
		}
	}

	return files
}

// resolveFile is the file Resolve finds for name, or "" when it finds none.
func resolveFile(t *testing.T, resolver *resolve.Resolver, name string) string {
	t.Helper()

	result, err := resolver.Resolve(name)
	if errors.Is(err, resolve.ErrModuleNotFound) {
		return ""
	}

	require.NoError(t, err, "Resolve(%q)", name)

	return result.FilePath
}

// describeFile names file relative to base for a failure message.
func describeFile(base, file string) string {
	if file == "" {
		return "nothing"
	}

	rel, err := filepath.Rel(base, file)
	if err != nil {
		return file
	}

	return filepath.ToSlash(rel)
}
