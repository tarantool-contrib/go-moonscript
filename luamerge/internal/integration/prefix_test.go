package integration_test

import (
	"maps"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/luatest"
)

// The tests in this file are about --package-prefix and --package-name, which
// bundle a library for other programs to require.

// TestCLI_PackagePrefixNameTaken: with --package-prefix p, x.lua required as
// "x" would be registered as "p.x", which is the name of p/x.lua. The bundle
// registers one module under a name, so one file would take the place of the
// other. The CLI refuses to bundle it, naming both files.
func TestCLI_PackagePrefixNameTaken(t *testing.T) {
	t.Parallel()

	dir := luatest.WriteProject(t, luatest.Project{
		"main.lua": "print((require(\"x\")), (require(\"p.x\")))\n",
		"x.lua":    "return \"X\"\n",
		"p/x.lua":  "return \"PX\"\n",
	})
	root, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	r := luatest.RunCLI(t, dir, nil, "--entry", "main.lua", "--package-prefix", "p", "--output", "-")

	assert.Equal(t, 1, r.ExitCode, "the build fails\nstderr:\n%s", r.Stderr)
	assert.Empty(t, r.Stdout, "no bundle is written")
	assert.Contains(t, r.Stderr, filepath.Join(root, "x.lua"), "the message names the module that gets the name")
	assert.Contains(t, r.Stderr, filepath.Join(root, "p", "x.lua"), "and the module that has it")
	assert.Contains(t, r.Stderr, "choose another prefix")
}

// testLibraryHost is a program that loads the library bundle mypkg, then its
// own utils. It prints the names the bundle added to the table package[arg[1]]
// (preload, or postload with --fallback), what the library says, what its
// own utils say, and what the library's module mypkg.utils says.
const testLibraryHost = `local registry = arg[1]
local before = {}
for name in pairs(package[registry] or {}) do before[name] = true end
local lib = require("mypkg")
local added = {}
for name in pairs(package[registry] or {}) do
  added[#added + 1] = not before[name] and name or nil
end
table.sort(added)
print("added: " .. table.concat(added, " "))
print("library: " .. lib.hello())
print("host: " .. require("utils").hello())
print("mypkg.utils: " .. require("mypkg.utils").hello())
`

// TestBundle_LibraryKeepsItsModulesToItself is the README's "Package a
// library" recipe: a library whose entry requires its own utils by the bare
// name, bundled with --package-name mypkg into mypkg.lua, and a program that
// requires it and then its own utils. The bundle used to register utils in
// package.preload next to mypkg.utils, where require looks before any file,
// so the program's own require("utils") got the library's module. Only
// prefixed names may leave the bundle, while the library still finds its
// utils; with --fallback, a copy of mypkg.utils on disk wins over the
// embedded one, as it does over any embedded module.
func TestBundle_LibraryKeepsItsModulesToItself(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	library := luatest.WriteProject(t, luatest.Project{
		"src/init.lua":  "local u = require(\"utils\")\nreturn { hello = u.hello }\n",
		"src/utils.lua": "return { hello = function() return \"LIBRARY utils\" end }\n",
	})
	host := luatest.Project{
		"main.lua":  testLibraryHost,
		"utils.lua": "return { hello = function() return \"APP utils\" end }\n",
	}

	tests := map[string]struct {
		flags    []string
		registry string
		// onDisk is added to the program's directory.
		onDisk luatest.Project
		want   string
	}{
		"default": {
			flags: nil, registry: "preload", onDisk: nil,
			want: "added: mypkg.utils\nlibrary: LIBRARY utils\nhost: APP utils\nmypkg.utils: LIBRARY utils\n",
		},
		"debug": {
			flags: []string{"--debug"}, registry: "preload", onDisk: nil,
			want: "added: mypkg.utils\nlibrary: LIBRARY utils\nhost: APP utils\nmypkg.utils: LIBRARY utils\n",
		},
		"fallback": {
			flags: []string{"--fallback"}, registry: "postload", onDisk: nil,
			want: "added: mypkg.utils\nlibrary: LIBRARY utils\nhost: APP utils\nmypkg.utils: LIBRARY utils\n",
		},
		"fallback, a copy of mypkg.utils on disk": {
			flags: []string{"--fallback"}, registry: "postload",
			onDisk: luatest.Project{
				"mypkg/utils.lua": "return { hello = function() return \"ON-DISK utils\" end }\n",
			},
			want: "added: mypkg.utils\nlibrary: ON-DISK utils\nhost: APP utils\nmypkg.utils: ON-DISK utils\n",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			args := append([]string{"--entry", "src/init.lua", "--package-name", "mypkg"}, tt.flags...)
			bundle := luatest.CLI(args...)(t, library)

			program := maps.Clone(host)
			maps.Copy(program, tt.onDisk)
			program["mypkg.lua"] = string(bundle)
			dir := luatest.WriteProject(t, program)

			luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
				t.Helper()

				r := luatest.Run(t, interp, dir, runOptions(), "main.lua", tt.registry)
				assert.Equal(t, 0, r.ExitCode, "%s", r)
				assert.Equal(t, tt.want, r.Stdout, "%s", r)
			})
		})
	}
}

// TestBundle_PackageNameGivesOneInstance: with --package-name p a library
// can require its module both by the bare name and by the prefixed one,
// which finds the same file once the prefix is stripped. Renamed, the two
// requires are of one name, p.x, the module's only name, so they give one
// instance, which sees p.x in `...`.
func TestBundle_PackageNameGivesOneInstance(t *testing.T) {
	t.Parallel()

	luatest.Interpreters(t)

	bundle := bundleProject(t, luatest.Project{
		"main.lua": "local a = require(\"x\")\nlocal b = require(\"p.x\")\n" +
			"print(\"runs=\" .. RUNS, \"same=\" .. tostring(a == b), a.name)\n",
		"x.lua": "RUNS = (RUNS or 0) + 1\nreturn { name = (...) }\n",
	}, "--entry", "main.lua", "--package-name", "p")

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		r := luatest.RunBundle(t, interp, bundle, runOptions())
		assert.Equal(t, 0, r.ExitCode, "%s", r)
		assert.Equal(t, "runs=1\tsame=true\tp.x\n", r.Stdout, "%s", r)
	})
}

// testRenamedProject requires modules of its own in every form of a call of
// require, through a local named require that is not the global one, and
// modules that are no files of the project, which the bundle leaves to the
// runtime.
func testRenamedProject() luatest.Project {
	return luatest.Project{
		"main.lua": `local a = require("a")
local b = require "b"
local c = require 'c'
local d = require[[d]]
local e = require [==[
e]==]
local f = require(("f"), "extra")
local g = _G.require("g")
local h = _G["require"]("h")
local ok, i = pcall(require, "i")
local ok2, j = xpcall(require, function() return { name = "no j" } end, "j")
local k = require("k"):get()
local l = require("./a")
print(a.name, b.name, c.name, d.name, e.name, f.name, g.name, h.name, i.name, j.name, k, l.name)
local function load(require) return require("sub.x") end
print(load(function(name) return "a local require got " .. name end))
print((require("sub.x")))
print(require("string") == string, (pcall(require, "not.in.the.project")))
`,
		"a.lua":     "return { name = \"a\" }\n",
		"b.lua":     "return { name = \"b\" }\n",
		"c.lua":     "return { name = \"c\" }\n",
		"d.lua":     "return { name = \"d\" }\n",
		"e.lua":     "return { name = \"e\" }\n",
		"f.lua":     "return { name = \"f\" }\n",
		"g.lua":     "return { name = \"g\" }\n",
		"h.lua":     "return { name = \"h\" }\n",
		"i.lua":     "return { name = \"i\" }\n",
		"j.lua":     "return { name = \"j\" }\n",
		"k.lua":     "return { get = function(self) return \"k\" end }\n",
		"sub/x.lua": "return \"sub.x, which requires \" .. require(\"a\").name .. \" and \" .. require(\"sub.y\")\n",
		"sub/y.lua": "return \"sub.y\"\n",
	}
}

// TestBundle_PackagePrefixRenamesRequires: under --package-prefix the bundle
// registers prefixed names only, so every require of a module of the project
// must be renamed to its prefixed name, in every form of the call and in
// every module, or it finds nothing from a directory without the project's
// files. What the bundle leaves to the runtime keeps its name, and so does
// the call of a local named require. The bundle, run from an empty
// directory, behaves like the program.
func TestBundle_PackagePrefixRenamesRequires(t *testing.T) {
	t.Parallel()

	for mode, flags := range map[string][]string{
		"default": nil, "debug": {"--debug"}, "fallback": {"--fallback"}, "minify": {"--minify"},
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			spec := cliCase(testRenamedProject(), append([]string{"--package-prefix", "p"}, flags...)...)
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				require.Equal(t, 0, orig.ExitCode, "the original\n%s", orig)
				luatest.AssertSame(t, orig, bundle)
			}

			luatest.Differential(t, spec)
		})
	}
}

// TestBundle_PackagePrefixKeepsLines: renaming a require changes its literal
// only, on the lines the literal spans, so a --debug bundle reports an error
// on the line of the source, in a module and in the entry, after renamed
// requires of every form, one of them over two lines. The transforms run on
// the renamed source and keep the lines too.
func TestBundle_PackagePrefixKeepsLines(t *testing.T) {
	t.Parallel()

	project := luatest.Project{
		"main.lua": "local boom = require [==[\nboom]==]\n" +
			"local a, b = require(\"a\"), require 'a' local c = require[[a]] -- \"a\"\n" +
			"print(\"the module raises on line \" .. select(2, pcall(boom.go)):match(\"boom%.lua:(%d+):\"))\n" +
			"local ok, err = pcall(function() error(\"here\") end)\n" +
			"print(\"the entry raises on line \" .. err:match(\":(%d+): here\"))\n",
		"boom.lua": "local a = require(\"a\")\nlocal b = require [[\na]]\n" +
			"local M = {}\nfunction M.go()\n  error(\"boom\")\nend\nreturn M\n",
		"a.lua": "return {}\n",
	}

	for mode, flags := range map[string][]string{
		"debug":                  {"--debug"},
		"debug, fallback":        {"--debug", "--fallback"},
		"debug, remove-comments": {"--debug", "--remove-comments"},
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			spec := cliCase(project, append([]string{"--package-prefix", "p"}, flags...)...)
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				want := "the module raises on line 6\nthe entry raises on line 5\n"
				require.Equal(t, want, orig.Stdout, "the original\n%s", orig)
				luatest.AssertSame(t, orig, bundle)
			}

			luatest.Differential(t, spec)
		})
	}
}
