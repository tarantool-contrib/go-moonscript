package transform

import (
	"fmt"

	"github.com/tarantool/go-luamerge/internal/lex"
)

type emptyLineTransformer struct{}

// Transform removes the lines that hold nothing but white space and keeps
// every other byte. Only the white space between tokens can make up such a
// line: a line inside a token (a long string or long comment, a string that
// goes on after "\" or "\z") belongs to it and stays. A header that the
// interpreter skips when it loads a file (see lex.Header) is kept.
func (t *emptyLineTransformer) Transform(source []byte) ([]byte, error) {
	tokens, err := lex.NewFile(source).Tokens()
	if err != nil {
		return nil, fmt.Errorf("remove empty lines: %w", err)
	}

	pos := lex.Header(source)
	out := append(make([]byte, 0, len(source)), source[:pos]...)
	// The header is not white space: the line it is on stays.
	before := pos > 0

	for _, tok := range tokens {
		out = appendGap(out, source[pos:tok.Start], before, true)
		out = append(out, source[tok.Start:tok.End]...)
		before = true
		pos = tok.End
	}

	return appendGap(out, source[pos:], before, false), nil
}

// appendGap appends gap, the white space between two tokens, without the
// lines it makes up alone. before and after tell whether something precedes
// and follows the gap on its first and last line: then the white space that
// ends the line before, with its line break, and the white space that starts
// the line after stay. Every line in between holds nothing but white space.
func appendGap(out, gap []byte, before, after bool) []byte {
	// The ends of the first and the last line break.
	firstEnd, lastEnd := -1, -1

	for i := 0; i < len(gap); {
		n := lex.LineBreak(gap, i)
		if n == 0 {
			i++

			continue
		}

		i += n
		lastEnd = i

		if firstEnd < 0 {
			firstEnd = i
		}
	}

	if firstEnd < 0 { // All on one line.
		if before || after {
			return append(out, gap...)
		}

		return out
	}

	if before {
		out = append(out, gap[:firstEnd]...)
	}

	if after {
		out = append(out, gap[lastEnd:]...)
	}

	return out
}
