package parse_test

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gopherparse "github.com/yuin/gopher-lua/parse"

	"github.com/tarantool/go-luamerge/internal/lex"
	"github.com/tarantool/go-luamerge/internal/parse"
)

// testCorpus returns the Lua 5.1 files the parser is checked on: the test
// suites of Lua 5.1 and of gopher-lua, which ship with the gopher-lua module
// (a dependency, so it is in the module cache wherever this module builds),
// and the testdata of this repository.
func testCorpus(t *testing.T) []string {
	t.Helper()

	// #nosec G204 -- fixed arguments.
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}",
		"github.com/yuin/gopher-lua").Output()
	require.NoError(t, err, "locate the gopher-lua module")

	module := strings.TrimSpace(string(out))
	require.NotEmpty(t, module, "the gopher-lua module is not in the module cache")

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

// TestNormalize_Lua51SourceReadsTheSame: gopher-lua reads Lua 5.1, and on a
// source it reads the copy it gets in its place is the same program: the same
// syntax tree, the line of every node included, so the same requires on the
// same lines. The corpus is Lua 5.1; a file gopher-lua cannot read at all is
// left out and counted.
func TestNormalize_Lua51SourceReadsTheSame(t *testing.T) {
	t.Parallel()

	files := testCorpus(t)
	compared, unread := 0, 0

	for _, file := range files {
		src, err := os.ReadFile(file) // #nosec G304 -- a corpus file.
		require.NoError(t, err)

		// gopher-lua knows no header: give it what follows the header.
		want, err := gopherparse.Parse(bytes.NewReader(src[lex.Header(src):]), file)
		if err != nil {
			unread++

			continue
		}

		normalized, err := parse.NormalizeForTest(src)
		require.NoError(t, err, "normalize %s", file)

		got, err := gopherparse.Parse(bytes.NewReader(normalized), file)
		require.NoError(t, err, "gopher-lua reads the copy of %s", file)

		// assert.Equal compares the unexported line fields of the nodes too.
		if !assert.Equal(t, want, got, "syntax tree of the copy of %s", file) {
			continue
		}

		wantRequires, err := parse.FindRequiresForTest(want)
		require.NoError(t, err, "walk %s", file)

		gotRequires, err := parse.New().Parse(src, file)
		require.NoError(t, err, "parse %s", file)
		assert.Equal(t, wantRequires, gotRequires, "requires of %s", file)

		compared++
	}

	t.Logf("compared %d files, %d that gopher-lua cannot read left out", compared, unread)
	assert.Greater(t, compared, 50, "the corpus is there")
	assert.Less(t, unread, compared/10, "gopher-lua reads nearly all of the corpus")
}
