package transform_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/luatest"
)

// TestMain lets tests here bundle through the CLI (luatest.CLI): luatest.Main
// removes the binary it builds.
func TestMain(m *testing.M) { luatest.Main(m) }

// transformFlags are the CLI flags of the source transforms.
func transformFlags() []string {
	return []string{"--remove-comments"}
}

// transformCase is a differential test of main.lua bundled with the CLI flags
// args, every other field at its default.
func transformCase(main string, args ...string) luatest.Case {
	return luatest.Case{
		Project: luatest.Project{"main.lua": main},
		Dir:     "",
		Entry:   "",
		Bundle:  luatest.CLI(append([]string{"--entry", "main.lua"}, args...)...),
		Args:    nil,
		Options: luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0},
		Check:   nil,
	}
}

// TestCLI_TransformsKeepProgramData: every transform must leave the program
// as it was. The transforms used to scan bytes without knowing Lua's lexical
// grammar and changed string values or broke loading: blank lines inside long
// strings were deleted, leveled long strings were taken for code, a string
// ending in an escaped backslash never closed, removing a comment glued its
// neighbours into one token, a nested "[[" was counted, and a line comment
// ran past a lone "\r" to the next "\n".
func TestCLI_TransformsKeepProgramData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		main string
	}{
		{
			name: "blank lines in a long string",
			main: "local help = [[\nUsage: tool [options]\n\nOptions:\n  -h   show help]]\nprint(#help)\n",
		},
		{
			name: "comment and spaces in a leveled long string",
			main: "local sql = [=[\nSELECT 1 -- the answer\n]=]\nio.write(sql)\nprint([=[a    b]=])\n",
		},
		{
			name: "comment start in a leveled long string",
			main: "print([==[http://example.com/a--b]==])\n",
		},
		{
			name: "string ending in an escaped backslash",
			main: "local sep = \"\\\\\"\nlocal banner = \"|    centered    |\"\nprint(sep, banner, #banner)\n",
		},
		{
			name: "block comment between a keyword and a name",
			main: "b = \"global\"\nlocal--[[c]]b = \"local\"\nprint(b, _G.b)\n",
		},
		{
			name: "block comment between a number and a concatenation",
			main: "print(1--[[one]]..2)\n",
		},
		{
			name: "long bracket opener inside a long string",
			main: "local open = [[ [[ ]]\nlocal close = \"]]\"\nlocal s = \"a -- b\"\nprint(open, close, s)\n",
		},
		{
			name: "line comment ended by a carriage return",
			main: "local x = 1 -- one\rprint(\"x is\", x)\rprint(\"done\")\r",
		},
	}

	for _, tt := range tests {
		for _, flag := range transformFlags() {
			t.Run(tt.name+"/"+flag, func(t *testing.T) {
				t.Parallel()

				luatest.Differential(t, transformCase(tt.main, flag))
			})
		}
	}
}

// errorLineRe finds the line of main.lua in an error message: "main.lua:4:"
// natively, "/abs/path/main.lua:4:" from a --debug bundle.
var errorLineRe = regexp.MustCompile(`main\.lua:(\d+): boom`)

// TestCLI_RemoveCommentsKeepsLines: a --debug bundle loads each module under
// its own file name, so an error names the line of the source. Removing
// comments must not move it: a removed comment keeps its line breaks. The
// block comment used to be removed with the line break inside it.
func TestCLI_RemoveCommentsKeepsLines(t *testing.T) {
	t.Parallel()

	main := "--[[ a block comment\nover two lines ]]\nlocal x = 1 -- one\nerror(\"boom \" .. x)\n"

	spec := transformCase(main, "--debug", "--remove-comments")
	spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
		t.Helper()

		luatest.AssertSame(t, orig, bundle)

		origLine := errorLineRe.FindStringSubmatch(orig.Stderr)
		require.NotNil(t, origLine, "the original's error names its line\n%s", orig)

		bundleLine := errorLineRe.FindStringSubmatch(bundle.Stderr)
		require.NotNil(t, bundleLine, "the bundle's error names the line of main.lua\n%s", bundle)

		assert.Equal(t, "4", origLine[1], "the error is on line 4 of main.lua\n%s", orig)
		assert.Equal(t, origLine[1], bundleLine[1], "the bundle reports the line of the source\n%s", bundle)
	}

	luatest.Differential(t, spec)
}

// TestCLI_TransformFlagsTakeEffect: each transform flag reaches its
// transform. A transform that is not applied at all keeps every program
// intact, so the differential tests cannot tell.
func TestCLI_TransformFlagsTakeEffect(t *testing.T) {
	t.Parallel()

	main := "local x = 1 -- MARK\n\n\nprint(x)\n"

	tests := []struct {
		flag   string
		module string
	}{
		{flag: "--remove-comments", module: "local x = 1 \n\n\nprint(x)\n"},
	}

	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			t.Parallel()

			dir := luatest.WriteProject(t, luatest.Project{"main.lua": main})

			result := luatest.RunCLI(t, dir, nil, "--entry", "main.lua", tt.flag)
			require.Equal(t, 0, result.ExitCode, result.Stderr)
			assert.Contains(t, result.Stdout, tt.module, "the module as the transform leaves it")
			assert.NotContains(t, result.Stdout, "MARK")
		})
	}
}
