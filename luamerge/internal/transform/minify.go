package transform

import (
	"bytes"
	"fmt"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

type minifyTransformer struct{}

// Transform keeps every token but the comments byte for byte and rebuilds
// what lies between them: nothing where the two tokens stay apart without it,
// a space where they would run together ("local x", "a - -b", "1 ..2"), and a
// line break where the source had one or more, in white space or inside a
// comment. No line break goes away and none is added: Lua 5.1 and LuaJIT
// refuse a call whose "(" starts a new line as ambiguous, where Lua 5.2+
// reads the call. The header that the interpreter skips when it loads a file
// (see lex.Header), a byte order mark and a first line starting with "#", is
// dropped. Line numbers are not kept.
func (t *minifyTransformer) Transform(source []byte) ([]byte, error) {
	tokens, err := lex.NewFile(source).Tokens()
	if err != nil {
		return nil, fmt.Errorf("minify: %w", err)
	}

	out := make([]byte, 0, len(source))
	pos := lex.Header(source)

	var prev []byte

	newline := false // A line break since prev.

	for _, tok := range tokens {
		text := source[tok.Start:tok.End]
		newline = newline || hasLineBreak(source[pos:tok.Start])
		pos = tok.End

		if isComment(tok) {
			newline = newline || tok.EndLine > tok.Line

			continue
		}

		if prev != nil {
			switch {
			case newline:
				out = append(out, '\n')
			case lex.NeedsSeparator(prev, text):
				out = append(out, ' ')
			}
		}

		out = append(out, text...)
		prev = text
		newline = false
	}

	if prev != nil && (newline || hasLineBreak(source[pos:])) {
		out = append(out, '\n')
	}

	return out, nil
}

// hasLineBreak reports whether text holds a line break.
func hasLineBreak(text []byte) bool {
	return bytes.ContainsAny(text, "\r\n")
}
