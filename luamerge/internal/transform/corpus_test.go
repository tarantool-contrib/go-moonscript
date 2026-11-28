package transform_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/luatest"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/transform"
)

// corpusTransform is a transform checked over the corpus.
type corpusTransform struct {
	name        string
	transformer transform.Transformer
	// keepsLines is set when the transform keeps every token on its line.
	keepsLines bool
}

// corpusTransforms are the transforms checked over the corpus.
func corpusTransforms() []corpusTransform {
	return []corpusTransform{
		{name: "remove-comments", transformer: &transform.CommentTransformerForTest{}, keepsLines: true},
		{name: "remove-empty-lines", transformer: &transform.EmptyLineTransformerForTest{}, keepsLines: false},
		{name: "minify", transformer: &transform.MinifyTransformerForTest{}, keepsLines: false},
	}
}

// testCorpus returns the absolute paths of the Lua files the transforms are
// checked on: the test suites of Lua 5.1 and of gopher-lua, which ship with
// the gopher-lua module v1.1.2, and the testdata of this repository. The fork
// of gopher-lua in lua/ has neither suite and is no module of its own, so the
// module comes from the module cache (go mod download fetches it when it is
// not there).
func testCorpus(t *testing.T) []string {
	t.Helper()

	module := gopherLuaModule(t)

	testdata, err := filepath.Abs(filepath.Join("..", "..", "testdata"))
	require.NoError(t, err)

	roots := []string{filepath.Join(module, "_lua5.1-tests"), filepath.Join(module, "_glua-tests"), testdata}

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

	require.Greater(t, len(files), 50, "the corpus is there")

	return files
}

// significant is the tokens of a Lua file other than comments, as this
// package's lexer reads them: kind and text, and the line when lines is set.
func significant(t *testing.T, src []byte, lines bool) []string {
	t.Helper()

	tokens, err := lex.NewFile(src).Tokens()
	require.NoError(t, err)

	var spelled []string

	for _, tok := range tokens {
		if tok.Kind == lex.Comment || tok.Kind == lex.LongComment {
			continue
		}

		text := fmt.Sprintf("%s %q", tok.Kind, src[tok.Start:tok.End])
		if lines {
			text += " line " + strconv.Itoa(tok.Line)
		}

		spelled = append(spelled, text)
	}

	return spelled
}

// gopherLuaModule returns the directory of the gopher-lua module v1.1.2 in
// the module cache.
func gopherLuaModule(t *testing.T) string {
	t.Helper()

	// #nosec G204 -- fixed arguments.
	out, err := exec.CommandContext(t.Context(), "go", "mod", "download", "-json",
		"github.com/yuin/gopher-lua@v1.1.2").Output()
	require.NoError(t, err, "download the gopher-lua module")

	var module struct {
		Dir string `json:"Dir"` //nolint:tagliatelle // the name go mod download -json gives it.
	}

	require.NoError(t, json.Unmarshal(out, &module), "read the output of go mod download -json")
	require.NotEmpty(t, module.Dir, "the gopher-lua module is not in the module cache")

	return module.Dir
}

// gopherTokens is what the lexer of the gopher-lua fork (lua/parse) reads
// from a Lua file in its LuaJIT dialect, which reads goto and labels as
// gopher-lua v1.1.2 did, header left out: kind and source text of each token
// other than trivia (white space, line breaks, comments), and its line when
// lines is set.
func gopherTokens(src []byte, lines bool) ([]string, error) {
	body := src[lex.Header(src):]

	options := parse.Options{Dialect: parse.LuaJIT, Comments: false, Shebang: false, Tokens: false, Lua52Compat: false}

	lexemes, err := parse.Tokenize(body, "corpus", options)
	if err != nil {
		return nil, fmt.Errorf("gopher-lua: %w", err)
	}

	positions := ast.NewLines(body)

	var tokens []string

	for _, lexeme := range lexemes {
		if lexeme.Kind.IsTrivia() || lexeme.Kind == ast.TokenEOF {
			continue
		}

		text := fmt.Sprintf("%s %q", lexeme.Kind, lexeme.Text(body))
		if lines {
			text += " line " + strconv.Itoa(positions.Pos(lexeme.Span().Start).Line)
		}

		tokens = append(tokens, text)
	}

	return tokens, nil
}

// assertSameTokensf asserts that two token lists are equal and names the
// first difference only: a corpus file has thousands of tokens.
func assertSameTokensf(t *testing.T, want, got []string, format string, args ...any) {
	t.Helper()

	what := fmt.Sprintf(format, args...)

	for i := range min(len(want), len(got)) {
		if want[i] != got[i] {
			assert.Failf(t, "tokens differ", "%s: token %d is %s, was %s", what, i, got[i], want[i])

			return
		}
	}

	assert.Len(t, got, len(want), "%s: number of tokens", what)
}

// TestTransformers_CorpusTokens: over the corpus, every transform keeps the
// tokens other than comments, byte for byte and in order, and those that
// keep lines keep each on its line. Two lexers judge it: this package's and,
// independently, the gopher-lua fork's.
func TestTransformers_CorpusTokens(t *testing.T) {
	t.Parallel()

	files := testCorpus(t)

	for _, trans := range corpusTransforms() {
		t.Run(trans.name, func(t *testing.T) {
			t.Parallel()

			gopherFiles := 0

			for _, file := range files {
				src, err := os.ReadFile(file) // #nosec G304 -- a corpus file.
				require.NoError(t, err)

				got, err := trans.transformer.Transform(src)
				require.NoError(t, err, file)

				assertSameTokensf(t, significant(t, src, trans.keepsLines), significant(t, got, trans.keepsLines),
					"%s: this lexer", file)

				want, err := gopherTokens(src, trans.keepsLines)
				if err != nil {
					t.Logf("%s: gopher-lua cannot read the original: %v", file, err)

					continue
				}

				theirs, err := gopherTokens(got, trans.keepsLines)
				require.NoError(t, err, "%s: gopher-lua reads the transformed source", file)
				assertSameTokensf(t, want, theirs, "%s: gopher-lua", file)

				gopherFiles++
			}

			t.Logf("%d files, %d of them also read by gopher-lua", len(files), gopherFiles)
			assert.Equal(t, len(files), gopherFiles, "gopher-lua reads every file of the corpus")
		})
	}
}

// dumpCompare is the Lua script of TestTransformers_CorpusBytecode. Its
// arguments are a mode and a file that lists "original<TAB>transformed"
// paths, one pair per line; it loads both files of each pair and compares
// their bytecode. In mode "full" the bytecode keeps its debug information,
// line numbers included; mode "strip" leaves it out, which only LuaJIT does
// completely (PUC Lua keeps the lines where functions start and end). Like
// the interpreter loading a file, it skips a byte order mark and a first line
// starting with "#", keeping the line count.
const dumpCompare = `local mode, list = arg[1], arg[2]
local strip = mode == "strip"
if strip and not jit then print("unsupported") return end
local load = loadstring or load
local function read(path)
  local file = assert(io.open(path, "rb"))
  local text = file:read("*a")
  file:close()
  return (text:gsub("^\239\187\191", ""):gsub("^#[^\n]*", ""))
end
local function verdict(original, transformed)
  local f, err = load(read(original), "=chunk")
  if not f then return "skip\t" .. err end
  local g, gerr = load(read(transformed), "=chunk")
  if not g then return "broken\t" .. gerr end
  if string.dump(f, strip) ~= string.dump(g, strip) then return "differ" end
  return "same"
end
for line in io.lines(list) do
  local original, transformed = line:match("^(.-)\t(.*)$")
  print(verdict(original, transformed) .. "\t" .. original)
end
`

// TestTransformers_CorpusBytecode: over the corpus, every transform leaves
// each file compiling to the same bytecode on every interpreter. A transform
// that keeps lines keeps the debug information too; for the others the
// bytecode is compared without it, on LuaJIT and Tarantool only.
func TestTransformers_CorpusBytecode(t *testing.T) {
	t.Parallel()

	files := testCorpus(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dumpcompare.lua"), []byte(dumpCompare), 0o600))

	for _, trans := range corpusTransforms() {
		require.NoError(t, os.Mkdir(filepath.Join(dir, trans.name), 0o750))

		var list strings.Builder

		for i, file := range files {
			src, err := os.ReadFile(file) // #nosec G304 -- a corpus file.
			require.NoError(t, err)

			got, err := trans.transformer.Transform(src)
			require.NoError(t, err, file)

			transformed := filepath.Join(dir, trans.name, strconv.Itoa(i)+".lua")
			// #nosec G703 -- a file of the test's temporary directory.
			require.NoError(t, os.WriteFile(transformed, got, 0o600))
			fmt.Fprintf(&list, "%s\t%s\n", file, transformed)
		}

		require.NoError(t, os.WriteFile(filepath.Join(dir, trans.name+".list"), []byte(list.String()), 0o600))
	}

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		for _, trans := range corpusTransforms() {
			mode := "strip"
			if trans.keepsLines {
				mode = "full"
			}

			opts := luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 2 * time.Minute}
			result := luatest.Run(t, interp, dir, opts, "dumpcompare.lua", mode, trans.name+".list")
			require.Equal(t, 0, result.ExitCode, "%s\n%s", trans.name, result)

			if strings.TrimSpace(result.Stdout) == "unsupported" {
				t.Logf("%s: %s cannot compare bytecode without line numbers", trans.name, interp.Name)

				continue
			}

			counts := map[string]int{}
			scanner := bufio.NewScanner(strings.NewReader(result.Stdout))

			for scanner.Scan() {
				verdict, rest, _ := strings.Cut(scanner.Text(), "\t")
				counts[verdict]++

				if verdict != "same" && verdict != "skip" {
					assert.Failf(t, "bytecode changed", "%s on %s: %s %s", trans.name, interp.Name, verdict, rest)
				}
			}

			t.Logf("%s on %s (%s bytecode): %d files the same, %d the interpreter cannot load",
				trans.name, interp.Name, mode, counts["same"], counts["skip"])
			assert.Equal(t, len(files), counts["same"]+counts["skip"]+counts["differ"]+counts["broken"],
				"%s on %s: every file was compared", trans.name, interp.Name)
			assert.Positive(t, counts["same"], "%s on %s: some file was compared", trans.name, interp.Name)
		}
	})
}
