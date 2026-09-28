package transform_test

import (
	"bufio"
	"bytes"
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
	"github.com/yuin/gopher-lua/ast"
	"github.com/yuin/gopher-lua/parse"

	"github.com/tarantool/go-luamerge/internal/lex"
	"github.com/tarantool/go-luamerge/internal/luatest"
	"github.com/tarantool/go-luamerge/internal/transform"
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
	}
}

// testCorpus returns the absolute paths of the Lua files the transforms are
// checked on: the test suites of Lua 5.1 and of gopher-lua, which ship with
// the gopher-lua module (a dependency, so it is in the module cache wherever
// this module builds), and the testdata of this repository.
func testCorpus(t *testing.T) []string {
	t.Helper()

	// #nosec G204 -- fixed arguments.
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}",
		"github.com/yuin/gopher-lua").Output()
	require.NoError(t, err, "locate the gopher-lua module")

	module := strings.TrimSpace(string(out))
	require.NotEmpty(t, module, "the gopher-lua module is not in the module cache")

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

// gopherTokens is what gopher-lua's scanner reads from a Lua file, header
// left out (gopher-lua knows none): type and value of each token, which for a
// string is its decoded value, and its line when lines is set.
func gopherTokens(src []byte, lines bool) ([]string, error) {
	scanner := parse.NewScanner(bytes.NewReader(src[lex.Header(src):]), "corpus")
	lexer := new(parse.Lexer) // Scan only keeps the previous token type in it.

	var tokens []string

	for {
		token, err := scanner.Scan(lexer)
		if err != nil {
			return nil, fmt.Errorf("gopher-lua: %w", err)
		}

		if token.Type == parse.EOF {
			return tokens, nil
		}

		lexer.PrevTokenType = token.Type
		tokens = append(tokens, spellGopher(token, lines))
	}
}

// spellGopher spells a gopher-lua token for comparison.
func spellGopher(token ast.Token, lines bool) string {
	text := fmt.Sprintf("%d %q", token.Type, token.Str)
	if lines {
		text += " line " + strconv.Itoa(token.Pos.Line)
	}

	return text
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
// independently, gopher-lua's, which also compares the decoded value of every
// string.
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
