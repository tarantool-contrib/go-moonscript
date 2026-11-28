package parse_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gopherparse "github.com/tarantool-contrib/go-moonscript/lua/parse"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
)

// testCorpus returns the Lua 5.1 files the parser is checked on: the test
// suites of Lua 5.1 and of gopher-lua, which ship with the gopher-lua module
// v1.1.2, and the testdata of this repository. The fork of gopher-lua in lua/
// has neither suite and is no module of its own, so the module comes from the
// module cache (go mod download fetches it when it is not there).
func testCorpus(t *testing.T) []string {
	t.Helper()

	// #nosec G204 -- fixed arguments.
	out, err := exec.CommandContext(t.Context(), "go", "mod", "download", "-json",
		"github.com/yuin/gopher-lua@v1.1.2").Output()
	require.NoError(t, err, "download the gopher-lua module")

	var downloaded struct {
		Dir string `json:"Dir"` //nolint:tagliatelle // the name go mod download -json gives it.
	}

	require.NoError(t, json.Unmarshal(out, &downloaded), "read the output of go mod download -json")
	require.NotEmpty(t, downloaded.Dir, "the gopher-lua module is not in the module cache")

	module := downloaded.Dir

	roots := []string{
		filepath.Join(module, "_lua5.1-tests"),
		filepath.Join(module, "_glua-tests"),
		filepath.Join("..", "..", "testdata"),
	}

	var files []string

	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if !entry.IsDir() && strings.HasSuffix(path, ".lua") {
				files = append(files, path)
			}

			return nil
		})
		require.NoError(t, err, "walk %s", root)
	}

	return files
}

// TestParse_Lua51CorpusIsRead: the corpus is Lua 5.1, and a file that the fork's
// parser reads in its Lua 5.1 dialect, as lua5.1 reads a file, Parse reads
// too, whichever dialect it reads it in: it has no fallback, and the literal
// of a static call is where Parse says, a string literal whose value is the
// module name. A file that the Lua 5.1 dialect cannot read is left out and
// counted.
func TestParse_Lua51CorpusIsRead(t *testing.T) {
	t.Parallel()

	files := testCorpus(t)
	compared, unread := 0, 0

	for _, file := range files {
		src, err := os.ReadFile(file) // #nosec G304 -- a corpus file.
		require.NoError(t, err)

		options := gopherparse.Options{Dialect: gopherparse.Lua51, Comments: false, Shebang: true, Tokens: false,
			Lua52Compat: false}

		_, err = gopherparse.ParseChunk(src, file, options)
		if err != nil {
			unread++

			continue
		}

		found, err := parse.New().Parse(src, file)
		require.NoError(t, err, "parse %s", file)
		assert.Nil(t, found.Fallback, "the parser reads %s", file)

		// The literal of a static call is where Parse says: a string literal
		// whose value is the module name.
		for _, call := range found.Requires {
			if call.Static {
				value, ok := parse.LiteralValueForTest(src[call.Literal.Start:call.Literal.End])
				assert.True(t, ok && value == call.Name, "%s: the literal of %+v", file, call)
			}
		}

		compared++
	}

	t.Logf("compared %d files, %d that Lua 5.1 cannot read left out", compared, unread)
	assert.Greater(t, compared, 50, "the corpus is there")
	assert.Less(t, unread, compared/10, "the parser reads nearly all of the corpus")
}

// TestScanRequires_FindsTheParsersCalls: over the corpus, the token scanner,
// which reads a file the parser cannot, finds every call of require the
// parser finds, with the same module name on the same line, named by the
// same literal.
func TestScanRequires_FindsTheParsersCalls(t *testing.T) {
	t.Parallel()

	files := testCorpus(t)
	calls, unread := 0, 0

	for _, file := range files {
		src, err := os.ReadFile(file) // #nosec G304 -- a corpus file.
		require.NoError(t, err)

		found, err := parse.New().Parse(src, file)
		require.NoError(t, err, "parse %s", file)

		if found.Fallback != nil {
			// A file LuaJIT does not compile (a goto into the scope of a
			// local, ...): the token scanner read it, there is nothing to
			// compare.
			t.Logf("the parser cannot read %s: %s", file, found.Fallback.Reason)

			unread++

			continue
		}

		scanned, err := parse.ScanRequiresForTest(src)
		require.NoError(t, err, "scan %s", file)

		for _, call := range found.Requires {
			if call.Indirect != "" {
				continue
			}

			calls++

			i := slices.Index(scanned, call)
			if assert.GreaterOrEqual(t, i, 0, "%s: the token scanner finds %+v", file, call) {
				scanned = slices.Delete(scanned, i, i+1)
			}
		}
	}

	t.Logf("the token scanner found the %d calls of require the parser finds, %d files the parser cannot read left out",
		calls, unread)
	assert.Positive(t, calls, "the corpus has calls of require")
	assert.Less(t, unread, len(files)/10, "the parser reads nearly all of the corpus")
}
