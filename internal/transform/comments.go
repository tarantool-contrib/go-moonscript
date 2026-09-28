// Package transform implements source-to-source transformations applied to
// merged Lua output, such as comment stripping, empty-line removal, shebang
// stripping and minification.
package transform

import (
	"fmt"

	"github.com/tarantool/go-luamerge/internal/lex"
)

type commentTransformer struct{}

// Transform removes every comment and keeps every other byte. A comment
// becomes the line breaks it contained, so each token stays on its line and a
// --debug traceback keeps its line numbers; where the tokens on both sides of
// a comment would otherwise run together, a space stays between them:
// "local--[[c]]b" becomes "local b", not "localb". A header that the
// interpreter skips when it loads a file (see lex.Header) is kept as it is.
func (t *commentTransformer) Transform(source []byte) ([]byte, error) {
	tokens, err := lex.NewFile(source).Tokens()
	if err != nil {
		return nil, fmt.Errorf("remove comments: %w", err)
	}

	pos := lex.Header(source)
	out := newRebuilder(source[:pos], len(source))

	var prev []byte

	for _, tok := range tokens {
		out.space(source[pos:tok.Start])
		text := source[tok.Start:tok.End]
		pos = tok.End

		if isComment(tok) {
			out.drop(text)

			continue
		}

		out.token(prev, text)
		prev = text
	}

	out.space(source[pos:])

	return out.bytes(), nil
}

// isComment reports whether tok is a comment.
func isComment(tok lex.Token) bool {
	return tok.Kind == lex.Comment || tok.Kind == lex.LongComment
}

// isLineBreakByte reports whether c is "\n" or "\r".
func isLineBreakByte(c byte) bool {
	return c == '\n' || c == '\r'
}

// rebuilder writes source with some of its bytes dropped, so that what
// remains reads as it did. Dropping bytes can join what they kept apart, so
// after a drop it separates, with a space:
//
//   - a line break from a line break of the other kind before it: "\r" and
//     "\n" would read as one line break, "\r" and " \n" read as two;
//   - a token from the token before it when the two would read as others
//     (see lex.NeedsSeparator).
type rebuilder struct {
	out []byte
	// cut is set when bytes were dropped after the last byte written.
	cut bool
	// afterToken is set when the last byte written ends a token.
	afterToken bool
}

// newRebuilder returns a rebuilder whose output starts with head and has room
// for size bytes.
func newRebuilder(head []byte, size int) *rebuilder {
	out := make([]byte, 0, size)

	return &rebuilder{out: append(out, head...), cut: false, afterToken: false}
}

// bytes returns what was written.
func (r *rebuilder) bytes() []byte {
	return r.out
}

// space writes white space.
func (r *rebuilder) space(text []byte) {
	for _, c := range text {
		r.whiteSpace(c)
	}
}

// drop leaves text out except for its line breaks.
func (r *rebuilder) drop(text []byte) {
	for _, c := range text {
		if isLineBreakByte(c) {
			r.whiteSpace(c)
		} else {
			r.cut = true
		}
	}
}

// whiteSpace writes one byte of white space.
func (r *rebuilder) whiteSpace(char byte) {
	if r.cut && isLineBreakByte(char) && len(r.out) > 0 {
		last := r.out[len(r.out)-1]
		if isLineBreakByte(last) && last != char {
			r.out = append(r.out, ' ')
		}
	}

	r.out = append(r.out, char)
	r.cut, r.afterToken = false, false
}

// token writes the token text; prev is the token written before it, if any.
func (r *rebuilder) token(prev, text []byte) {
	if r.cut && r.afterToken && lex.NeedsSeparator(prev, text) {
		r.out = append(r.out, ' ')
	}

	r.out = append(r.out, text...)
	r.cut, r.afterToken = false, true
}
