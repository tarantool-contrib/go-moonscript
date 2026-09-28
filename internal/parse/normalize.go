package parse

import (
	"bytes"

	"github.com/tarantool/go-luamerge/internal/lex"
)

// normalizer builds the copy of a Lua source that gopher-lua, a parser of Lua
// 5.1, reads in its place: the tokens of the source in their order and on
// their lines, each written the way Lua 5.1 reads it, with only spaces and
// line breaks between them. The copy is read for its calls of require only:
// the bundle embeds the source itself.
type normalizer struct {
	src []byte
	// tokens are the tokens of src without its comments.
	tokens []lex.Token
	// out is the copy so far.
	out []byte
	// line is the line the end of out is on.
	line int
}

// normalize returns the copy of src that gopher-lua reads: every token on the
// line it is on in src, so gopher-lua's lines are the lines of src, and every
// string literal, which may use escapes Lua 5.1 does not have or span lines,
// written as a literal of one line that Lua 5.1 reads as its value (see
// stringValue). Comments are left out. White space between tokens becomes a
// space or line breaks, as gopher-lua knows no form feed or vertical tab.
//
// src is read the way the interpreter reads a file it loads (see
// lex.NewFile). The error is a lexical error, of source no dialect reads.
func normalize(src []byte) ([]byte, error) {
	all, err := lex.NewFile(src).Tokens()
	if err != nil {
		return nil, err //nolint:wrapcheck // a *lex.Error, which says what went wrong and where.
	}

	tokens := make([]lex.Token, 0, len(all))

	for _, tok := range all {
		if tok.Kind != lex.Comment && tok.Kind != lex.LongComment {
			tokens = append(tokens, tok)
		}
	}

	n := &normalizer{src: src, tokens: tokens, out: make([]byte, 0, len(src)), line: 1}

	for _, tok := range n.tokens {
		n.token(tok)
	}

	return n.out, nil
}

// token writes the copy of tok.
func (n *normalizer) token(tok lex.Token) {
	text := n.src[tok.Start:tok.End]

	if tok.Kind == lex.String || tok.Kind == lex.LongString {
		text = quote(stringValue(text, tok.Kind))
	}

	n.write(tok.Line, text)
}

// write writes text as a token on line: after the line breaks that take the
// copy to line, or after a space on the line the copy is on.
func (n *normalizer) write(line int, text []byte) {
	switch {
	case n.line < line:
		n.out = append(n.out, bytes.Repeat([]byte("\n"), line-n.line)...)
		n.line = line
	case len(n.out) > 0:
		n.out = append(n.out, ' ')
	}

	n.out = append(n.out, text...)
}
