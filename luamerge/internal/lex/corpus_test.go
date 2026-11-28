package lex_test

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
	"github.com/yuin/gopher-lua/ast"
	"github.com/yuin/gopher-lua/parse"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

// testCorpus returns the Lua files the lexer is checked on: the test suites
// of Lua 5.1 and of gopher-lua, which ship with the gopher-lua module (a
// dependency, so it is in the module cache wherever this module builds), and
// the testdata of this repository.
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

// lineEndings rewrites a source's "\n" line breaks in the other styles Lua
// knows, so every file is also read with CR LF, CR and LF CR line endings.
func lineEndings() map[string]func(src []byte) []byte {
	with := func(lineBreak string) func(src []byte) []byte {
		return func(src []byte) []byte { return bytes.ReplaceAll(src, []byte("\n"), []byte(lineBreak)) }
	}

	return map[string]func(src []byte) []byte{
		"LF":    func(src []byte) []byte { return src },
		"CR LF": with("\r\n"),
		"CR":    with("\r"),
		"LF CR": with("\n\r"),
	}
}

// gopherTokens reads src with gopher-lua's scanner, which leaves comments out.
func gopherTokens(src []byte) ([]ast.Token, error) {
	scanner := parse.NewScanner(bytes.NewReader(src), "corpus")
	lexer := new(parse.Lexer) // Scan only keeps the previous token type in it.

	var tokens []ast.Token

	for {
		token, err := scanner.Scan(lexer)
		if err != nil {
			return nil, err
		}

		if token.Type == parse.EOF {
			return tokens, nil
		}

		lexer.PrevTokenType = token.Type
		tokens = append(tokens, token)
	}
}

// gopherText is the source text of a name or symbol gopher-lua read. Only "."
// comes without it.
func gopherText(token ast.Token) string {
	if token.Str == "" && token.Type == '.' {
		return "."
	}

	return token.Str
}

// sameAsGopher reports whether mine, whose source text is text, is the token
// gopher-lua read. Strings and numerals are compared by kind: gopher-lua keeps
// the decoded value of a string, and drops the leading zeros of a numeral.
func sameAsGopher(mine lex.Token, text string, theirs ast.Token) bool {
	if mine.Line != theirs.Pos.Line {
		return false
	}

	switch mine.Kind {
	case lex.String, lex.LongString:
		return theirs.Type == parse.TString
	case lex.Number:
		return theirs.Type == parse.TNumber
	case lex.Name, lex.Symbol:
		return theirs.Type != parse.TString && theirs.Type != parse.TNumber && text == gopherText(theirs)
	case lex.EOF, lex.Comment, lex.LongComment, lex.Invalid:
		return false
	default:
		return false
	}
}

// TestLexer_Tokens_MatchGopherLua checks the lexer against an independent
// one: over the corpus, in every line-ending style, it reads the same tokens
// on the same lines as gopher-lua's scanner, comments left out. gopher-lua
// reads Lua 5.1 (with goto); every file of the corpus is Lua 5.1.
func TestLexer_Tokens_MatchGopherLua(t *testing.T) {
	t.Parallel()

	files := testCorpus(t)
	compared, tokens := 0, 0

	for _, file := range files {
		src, err := os.ReadFile(file) // #nosec G304 -- a corpus file.
		require.NoError(t, err)

		// gopher-lua knows no header: give both lexers what follows it.
		body := src[lex.Header(src):]

		for style, convert := range lineEndings() {
			converted := convert(body)

			theirs, err := gopherTokens(converted)
			require.NoError(t, err, "gopher-lua reads %s (%s)", file, style)

			mine, err := lex.New(converted).Tokens()
			require.NoError(t, err, "lex %s (%s)", file, style)

			var significant []lex.Token

			for _, token := range mine {
				if token.Kind != lex.Comment && token.Kind != lex.LongComment {
					significant = append(significant, token)
				}
			}

			if !assert.Len(t, significant, len(theirs), "tokens of %s (%s)", file, style) {
				continue
			}

			for i, token := range significant {
				text := string(converted[token.Start:token.End])
				if !assert.True(t, sameAsGopher(token, text, theirs[i]),
					"%s (%s): token %d is %s %q on line %d, gopher-lua reads %q (type %d) on line %d",
					file, style, i, token.Kind, text, token.Line,
					theirs[i].Str, theirs[i].Type, theirs[i].Pos.Line) {
					break
				}
			}

			compared++
			tokens += len(significant)
		}
	}

	t.Logf("compared %d files in %d line-ending styles, %d tokens", len(files), len(lineEndings()), tokens)
	assert.Equal(t, len(files)*len(lineEndings()), compared, "every file was compared in every style")
	assert.Greater(t, len(files), 50, "the corpus is there")
}
