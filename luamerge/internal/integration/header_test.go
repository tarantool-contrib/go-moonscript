package integration_test

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/luatest"
)

// headerCase is a Lua file that starts with what the interpreter skips when
// it loads the file, a byte order mark or a first line starting with "#", or
// with white space, and raises "boom" on a known line.
type headerCase struct {
	name string
	// file is the file that starts so: m.lua, which main.lua requires, or
	// main.lua, the entry.
	file string
	// source is the contents of file.
	source string
	// line is the line of the error, as the interpreter counts it.
	line int
	// flags are more CLI flags for the bundler.
	flags []string
}

// project is the project of the case: file, and main.lua requiring it.
func (tc headerCase) project() luatest.Project {
	if tc.file == "main.lua" {
		return luatest.Project{"main.lua": tc.source}
	}

	return luatest.Project{"main.lua": "require(\"m\")\n", tc.file: tc.source}
}

// skipNative skips the subtest where the interpreter cannot load the original:
// Lua 5.1 does not skip a byte order mark, so there a file that starts with
// one fails natively ("unexpected symbol").
func (tc headerCase) skipNative(t *testing.T, orig luatest.Result) {
	t.Helper()

	if strings.HasPrefix(tc.source, lex.BOM) && isPUCLua51(orig.Interp) {
		t.Skipf("%s does not skip a byte order mark: the original does not load", orig.Interp.Name)
	}
}

// headerCases are files whose first lines hold no code.
func headerCases() []headerCase {
	return []headerCase{
		{name: "shebang", file: "m.lua", source: "#!/usr/bin/env lua\nerror(\"boom\")\n", line: 2, flags: nil},
		{
			name: "shebang with CRLF line breaks", file: "m.lua",
			source: "#!/usr/bin/env lua\r\nerror(\"boom\")\r\n", line: 2, flags: nil,
		},
		{name: "blank lines", file: "m.lua", source: "\n  \n\terror(\"boom\")\n", line: 3, flags: nil},
		// Lua reads "\n\r" as one line break: the line break a --debug bundle
		// puts after the opening bracket of the string that holds this file
		// must not take its first one along.
		{name: "blank line ended by CR", file: "m.lua", source: "\rerror(\"boom\")\r", line: 2, flags: nil},
		{
			name: "first line starting with # but not #!", file: "m.lua",
			source: "# not a shebang\nerror(\"boom\")\n", line: 2, flags: nil,
		},
		{name: "byte order mark", file: "m.lua", source: lex.BOM + "\n\nerror(\"boom\")\n", line: 3, flags: nil},
		{
			name: "byte order mark and shebang", file: "m.lua",
			source: lex.BOM + "#!/usr/bin/env lua\n\nerror(\"boom\")\n", line: 3, flags: nil,
		},
		{
			name: "entry with a shebang", file: "main.lua",
			source: "#!/usr/bin/env lua\n\nlocal x = 1\nerror(\"boom\")\n", line: 4, flags: nil,
		},
		{
			name: "shebang and a block comment, comments removed", file: "m.lua",
			source: "#!/usr/bin/env lua\n--[[ a block comment\nover two lines ]]\nerror(\"boom\")\n", line: 4,
			flags: []string{"--remove-comments"},
		},
	}
}

// errorPositionRe finds the position of the error in the first line of
// stderr: "lua: ./m.lua:3: boom" natively, "lua: /abs/path/m.lua:3: boom" or
// "lua: ...ath/m.lua:3: boom" (LUA_IDSIZE) from a --debug bundle; Tarantool
// prints no "lua: " and the absolute path.
var errorPositionRe = regexp.MustCompile(`(\S+):(\d+): boom`)

// errorPosition is the file name and the line the first line of r's stderr
// reports the error at.
func errorPosition(t *testing.T, r luatest.Result) (string, int) {
	t.Helper()

	first, _, _ := strings.Cut(r.Stderr, "\n")

	match := errorPositionRe.FindStringSubmatch(first)
	require.NotNil(t, match, "the first line of stderr names the error's position\n%s", r)

	line, err := strconv.Atoi(match[2])
	require.NoError(t, err)

	return filepath.Base(match[1]), line
}

// TestIntegration_FileHeader: a file that starts with a byte order mark, a
// first line starting with "#" or blank lines bundles and runs like the
// original. The interpreter skips the first two when it loads a file, and
// the bundle is no file of the project: a module and the entry must reach it
// without them. A byte order mark and a first line starting with "#" but not
// "#!" used to fail the build.
func TestIntegration_FileHeader(t *testing.T) {
	t.Parallel()

	for _, tt := range headerCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := cliCase(tt.project(), tt.flags...)
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				tt.skipNative(t, orig)
				luatest.AssertSame(t, orig, bundle)
			}

			luatest.Differential(t, spec)
		})
	}
}

// TestDebugTraceback_FileHeader: in a --debug bundle an error names the file
// and the line of the source, as natively, whatever the file starts with.
// The interpreter compiles a first line starting with "#" as an empty line,
// so the lines after it keep their numbers; the bundler used to delete that
// line with its line break, and every blank line before the first token, and
// so reported an error on line 3 after two blank lines on line 1.
func TestDebugTraceback_FileHeader(t *testing.T) {
	t.Parallel()

	for _, tt := range headerCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := cliCase(tt.project(), append([]string{"--debug"}, tt.flags...)...)
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				tt.skipNative(t, orig)
				luatest.AssertSame(t, orig, bundle)

				origFile, origLine := errorPosition(t, orig)
				require.Equal(t, tt.file, origFile, "the original's error names the file\n%s", orig)
				require.Equal(t, tt.line, origLine, "the original's error is on line %d\n%s", tt.line, orig)

				bundleFile, bundleLine := errorPosition(t, bundle)
				assert.Equal(t, tt.file, bundleFile, "the bundle's error names the file of the source\n%s", bundle)
				assert.Equal(t, origLine, bundleLine, "the bundle's error names the line of the source\n%s", bundle)
			}

			luatest.Differential(t, spec)
		})
	}
}
