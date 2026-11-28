package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/luatest"
)

// TestDebugBundle_SourceEndings: a --debug bundle embeds the code of each
// file in a long string, which must end where the code ends, whatever its
// last bytes are. Code that ended in "]" without a line break after it, such
// as `return t[1]`, formed the string's closing bracket together with the
// bracket the bundler put after it, and the bundle did not parse; so did
// code ending in "]=" or "]==" once the string needed that level, and code
// that --remove-comments or --minify left ending in "]".
func TestDebugBundle_SourceEndings(t *testing.T) {
	t.Parallel()

	main := "print((require(\"m\")))\n"
	tests := []struct {
		name    string
		project luatest.Project
		flags   []string
	}{
		{
			name:    "entry ending in ]",
			project: luatest.Project{"main.lua": "local t = {42}; print(t[1]); return t[1]"},
			flags:   nil,
		},
		{
			name:    "module ending in ]",
			project: luatest.Project{"main.lua": main, "m.lua": "local t = {7}\nreturn t[1]"},
			flags:   nil,
		},
		{
			name:    "module ending in ]= with ]] inside",
			project: luatest.Project{"main.lua": main, "m.lua": "return [[x]] -- ]="},
			flags:   nil,
		},
		{
			name:    "module ending in ]== with ]] and ]=] inside",
			project: luatest.Project{"main.lua": main, "m.lua": "return [[x]] .. [=[y]=] -- ]=="},
			flags:   nil,
		},
		{
			name:    "module ending in a leveled long string",
			project: luatest.Project{"main.lua": main, "m.lua": "return [=[x]=]"},
			flags:   nil,
		},
		{
			name:    "module ending in ] once its comment is removed",
			project: luatest.Project{"main.lua": main, "m.lua": "local t = {5}\nreturn t[1]--c"},
			flags:   []string{"--remove-comments"},
		},
		{
			name:    "module ending in ] once minified",
			project: luatest.Project{"main.lua": main, "m.lua": "local t = {5}\nreturn t[1] -- the end"},
			flags:   []string{"--minify"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := cliCase(tt.project, append([]string{"--debug"}, tt.flags...)...)
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				require.Equal(t, 0, orig.ExitCode, "the original runs\n%s", orig)
				luatest.AssertSame(t, orig, bundle)
			}

			luatest.Differential(t, spec)
		})
	}
}

// tracebackCase is a program that dies of an uncaught error, run natively
// from the root, where the program is meant to run from.
type tracebackCase struct {
	name    string
	project luatest.Project
	// args bundle the project, run in the project directory.
	args []string
	// dir is the root, where the original runs, relative to the project
	// directory.
	dir string
	// script is the entry, as the original's command line names it.
	script string
	// luaPath is the LUA_PATH of the original; "" is the default.
	luaPath string
	// position is where the error is raised, as the interpreter names it.
	position string
}

// firstLine is the first line of text.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")

	return line
}

// TestDebugBundle_ErrorNamesTheSourcePosition: an uncaught error in a --debug
// bundle is reported at the file and line the original program reports when
// it runs from the root: the entry named as the command line names it
// (`main.lua:2:`), and a module as require names the file it finds through
// package.path (`./lib/utils.lua:3:`), outside the root too. The chunks were
// named after the absolute paths of the files on the machine that built the
// bundle.
func TestDebugBundle_ErrorNamesTheSourcePosition(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	callNil := "return { boom = function()\n  local x = nil\n  x()\nend }\n"
	indexNil := "local t = nil\nprint(t.x)\n"
	tests := []tracebackCase{
		{
			name:     "module",
			project:  luatest.Project{"main.lua": "require(\"m\").boom()\n", "m.lua": callNil},
			args:     []string{"--entry", "main.lua"},
			dir:      ".",
			script:   "main.lua",
			luaPath:  "",
			position: "./m.lua:3: ",
		},
		{
			name:     "module in a directory",
			project:  luatest.Project{"main.lua": "require(\"lib.utils\")\n", "lib/utils.lua": "\n\nerror(\"boom\")\n"},
			args:     []string{"--entry", "main.lua"},
			dir:      ".",
			script:   "main.lua",
			luaPath:  "",
			position: "./lib/utils.lua:3: ",
		},
		{
			name:     "init module",
			project:  luatest.Project{"main.lua": "require(\"pkg\")\n", "pkg/init.lua": "error(\"boom\")\n"},
			args:     []string{"--entry", "main.lua"},
			dir:      ".",
			script:   "main.lua",
			luaPath:  "",
			position: "./pkg/init.lua:1: ",
		},
		{
			name:     "entry",
			project:  luatest.Project{"main.lua": indexNil},
			args:     []string{"--entry", "main.lua"},
			dir:      ".",
			script:   "main.lua",
			luaPath:  "",
			position: "main.lua:2: ",
		},
		{
			name:     "entry in a directory",
			project:  luatest.Project{"src/main.lua": indexNil},
			args:     []string{"--entry", "src/main.lua", "--root", "."},
			dir:      ".",
			script:   "src/main.lua",
			luaPath:  "",
			position: "src/main.lua:2: ",
		},
		{
			name:     "module in a search directory outside the root",
			project:  luatest.Project{"src/main.lua": "require(\"m\")\n", "lib/m.lua": "error(\"boom\")\n"},
			args:     []string{"--entry", "src/main.lua", "--search", "lib"},
			dir:      "src",
			script:   "main.lua",
			luaPath:  "./?.lua;../lib/?.lua",
			position: "../lib/m.lua:1: ",
		},
		{
			name:     "entry outside the root",
			project:  luatest.Project{"app/main.lua": "require(\"m\")\n" + indexNil, "lib/m.lua": "return {}\n"},
			args:     []string{"--entry", "app/main.lua", "--root", "lib"},
			dir:      "lib",
			script:   "../app/main.lua",
			luaPath:  "",
			position: "../app/main.lua:3: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := luatest.WriteProject(t, tt.project)
			bundle := luatest.CLI(append([]string{"--debug"}, tt.args...)...)(t, dir)
			origOpts := runOptions()
			origOpts.LuaPath = tt.luaPath

			luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
				t.Helper()

				orig := luatest.Run(t, interp, filepath.Join(dir, tt.dir), origOpts, tt.script)
				bun := luatest.RunBundle(t, interp, bundle, runOptions())
				origLine, bundleLine := firstLine(orig.Stderr), firstLine(bun.Stderr)

				require.NotEqual(t, 0, orig.ExitCode, "the original must fail\n%s", orig)
				assert.NotEqual(t, 0, bun.ExitCode, "the bundle must fail too\n%s", bun)
				assert.Contains(t, bundleLine, tt.position, "the bundle\n%s", bun)

				if strings.HasPrefix(interp.Version, "Tarantool") && strings.HasPrefix(tt.position, "./") {
					// Tarantool finds a module in the script's directory or in
					// the working directory itself and names it by its
					// absolute path, which the message may cut short with
					// "...". A bundle cannot know where it will run: it names
					// the file as require does through package.path. The rest
					// of the line agrees.
					assert.True(t, strings.HasSuffix(origLine, strings.TrimPrefix(bundleLine, ".")),
						"the original names the same file and line\n=== original: %s=== bundle: %s", orig, bun)

					return
				}

				require.Contains(t, origLine, tt.position, "the original\n%s", orig)
				assert.Equal(t, origLine, bundleLine,
					"the first line of the error\n=== original: %s=== bundle: %s", orig, bun)
			})
		})
	}
}

// TestDebugBundle_SearchDirectoryThroughALink: a search directory reached
// through a symbolic link, its own or the working directory's, names its
// modules as its resolved spelling does. The links stayed in the modules'
// paths, so their chunk names climbed out of the root to them:
// `../vlink/v.lua` for `--search vlink`, and for `--search vendor` run from a
// link to the project, a path to the working directory as the shell spells
// it (in PWD, which the Go runtime prefers).
func TestDebugBundle_SearchDirectoryThroughALink(t *testing.T) {
	t.Parallel()

	dir, err := filepath.EvalSymlinks(luatest.WriteProject(t, luatest.Project{
		"src/main.lua": "print(require(\"v\"))\n",
		"vendor/v.lua": "return \"v\"\n",
	}))
	require.NoError(t, err)
	require.NoError(t, os.Symlink("vendor", filepath.Join(dir, "vlink")))

	link := filepath.Join(t.TempDir(), "project")
	require.NoError(t, os.Symlink(dir, link))

	bundle := func(t *testing.T, wd, search string) string {
		t.Helper()

		r := luatest.RunCLI(t, wd, []string{"PWD=" + wd}, "--entry", "src/main.lua", "--search", search, "--debug")
		require.Equal(t, 0, r.ExitCode, "luamerge in %s with --search %s\n%s", wd, search, r.Stderr)

		return r.Stdout
	}

	want := bundle(t, dir, "vendor")
	require.Contains(t, want, "\"@../vendor/v.lua\"", "the module's chunk name")

	tests := map[string]struct{ wd, search string }{
		"search directory through a link":  {wd: dir, search: "vlink"},
		"working directory through a link": {wd: link, search: "vendor"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, want, bundle(t, tt.wd, tt.search))
		})
	}
}

// TestDebugBundle_SameForEveryCheckout: two checkouts of one project in
// different directories build the same --debug bundle, which holds neither
// directory. The chunk names were the absolute paths of the files, so each
// checkout built a bundle of its own, and every bundle disclosed where it was
// built. A module installed outside the project, which only an absolute
// template of --path reaches, is named by its absolute path: its path from
// the root climbed out of the checkout, so it depended on how deep the
// checkout lay, and the second checkout lies deeper than the first.
func TestDebugBundle_SameForEveryCheckout(t *testing.T) {
	t.Parallel()

	project := luatest.Project{
		"src/main.lua": "require(\"m\")\nrequire(\"lib.utils\")\nrequire(\"pkg\")\nrequire(\"v\")\n" +
			"require(\"installed\")\n",
		"src/m.lua":         "return \"m\"\n",
		"src/lib/utils.lua": "return \"utils\"\n",
		"src/pkg/init.lua":  "return \"pkg\"\n",
		"vendor/v.lua":      "return \"v\"\n",
	}
	deeper := make(luatest.Project, len(project))

	for path, source := range project {
		deeper["one/more/"+path] = source
	}

	system := luatest.WriteProject(t, luatest.Project{"installed.lua": "return \"installed\"\n"})
	bundler := luatest.CLI("--entry", "src/main.lua", "--search", "vendor",
		"--path", "?.lua;?/init.lua;"+filepath.Join(system, "?.lua"), "--debug")
	dirs := []string{luatest.WriteProject(t, project), filepath.Join(luatest.WriteProject(t, deeper), "one", "more")}

	bundles := []string{string(bundler(t, dirs[0])), string(bundler(t, dirs[1]))}

	assert.Equal(t, bundles[0], bundles[1], "two checkouts build the same bundle")
	assert.Contains(t, bundles[0], "\"@"+filepath.ToSlash(filepath.Join(system, "installed.lua"))+"\"",
		"a module only an absolute template reaches is named by its absolute path")

	for i, bundle := range bundles {
		for _, dir := range dirs {
			assert.NotContains(t, bundle, dir, "bundle %d holds a checkout's directory", i)
		}
	}
}

// TestDebugBundle_InstalledModuleName: a module installed outside the
// project, which only an absolute template of --path reaches, has the chunk
// name the same template in LUA_PATH gives it natively: its absolute path.
// The module prints its own chunk name, which, unlike the position in an
// error message, Lua does not cut short. It was a path from the root that
// climbed out of the checkout to the module.
func TestDebugBundle_InstalledModuleName(t *testing.T) {
	t.Parallel()

	system := luatest.WriteProject(t, luatest.Project{"installed.lua": "print(debug.getinfo(1, \"S\").source)\n"})
	template := filepath.Join(system, "?.lua")

	spec := cliCase(luatest.Project{"main.lua": "require(\"installed\")\n"},
		"--path", "?.lua;?/init.lua;"+template, "--debug")
	spec.Options.LuaPath = "./?.lua;./?/init.lua;" + template
	spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
		t.Helper()

		require.Equal(t, "@"+filepath.Join(system, "installed.lua")+"\n", orig.Stdout, "the original\n%s", orig)
		luatest.AssertSame(t, orig, bundle)
	}

	luatest.Differential(t, spec)
}
