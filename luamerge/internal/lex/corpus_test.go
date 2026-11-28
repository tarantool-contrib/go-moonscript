package lex_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

// testCorpus returns the Lua files the lexer is checked on: the test suites
// of Lua 5.1 and of gopher-lua, which ship with the gopher-lua module v1.1.2,
// and the testdata of this repository. The fork of gopher-lua in lua/ has
// neither suite and is no module of its own, so the module comes from the
// module cache (go mod download fetches it when it is not there).
func testCorpus(t *testing.T) []string {
	t.Helper()

	module := gopherLuaModule(t)

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

// gopherToken is a token the lexer of the gopher-lua fork read: its kind, its
// source text and the line it starts on.
type gopherToken struct {
	kind ast.TokenKind
	text string
	line int
}

// gopherTokens reads src with the lexer of the gopher-lua fork (lua/parse) in
// its LuaJIT dialect, which reads goto and labels as gopher-lua v1.1.2 did,
// and leaves out the trivia (white space, line breaks, comments) and the end
// of the source.
func gopherTokens(src []byte) ([]gopherToken, error) {
	options := parse.Options{Dialect: parse.LuaJIT, Comments: false, Shebang: false, Tokens: false}

	lexemes, err := parse.Tokenize(src, "corpus", options)
	if err != nil {
		return nil, err
	}

	lines := ast.NewLines(src)

	var tokens []gopherToken

	for _, lexeme := range lexemes {
		if lexeme.Kind.IsTrivia() || lexeme.Kind == ast.TokenEOF {
			continue
		}

		tokens = append(tokens, gopherToken{
			kind: lexeme.Kind,
			text: string(lexeme.Text(src)),
			line: lines.Pos(lexeme.Span().Start).Line,
		})
	}

	return tokens, nil
}

// sameAsGopher reports whether mine, whose source text is text, is the token
// the fork read: the same text on the same line, and a string or a numeral
// where the fork read one.
func sameAsGopher(mine lex.Token, text string, theirs gopherToken) bool {
	if mine.Line != theirs.line || text != theirs.text {
		return false
	}

	switch mine.Kind {
	case lex.String, lex.LongString:
		return theirs.kind == ast.TokenString
	case lex.Number:
		return theirs.kind == ast.TokenNumber
	case lex.Name, lex.Symbol:
		return theirs.kind != ast.TokenString && theirs.kind != ast.TokenNumber
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
					"%s (%s): token %d is %s %q on line %d, gopher-lua reads %q (%s) on line %d",
					file, style, i, token.Kind, text, token.Line,
					theirs[i].text, theirs[i].kind, theirs[i].line) {
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
