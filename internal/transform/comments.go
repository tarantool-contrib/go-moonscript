// Package transform implements source-to-source transformations applied to
// merged Lua output, such as comment stripping, empty-line removal, shebang
// stripping and minification.
package transform

import (
	"bytes"
)

// commentPrefixLen is the length of the Lua line/block comment prefix "--".
const commentPrefixLen = 2

// closeBracketExtra is the number of bracket characters ("[" and "]")
// surrounding the run of "=" signs in a long-bracket close pattern.
const closeBracketExtra = 2

type commentTransformer struct{}

func (t *commentTransformer) Transform(source []byte) ([]byte, error) {
	var out bytes.Buffer

	i := 0
	n := len(source)

	for i < n {
		if isCommentStart(source, i, n) {
			i = skipComment(source, i, n, &out)
			continue
		}

		if source[i] == '"' || source[i] == '\'' {
			i = copyString(source, i, n, &out)
			continue
		}

		if isLongBracketStart(source, i, n) {
			i = copyLongBracket(source, i, n, &out)
			continue
		}

		// Normal character.
		out.WriteByte(source[i])
		i++
	}

	return out.Bytes(), nil
}

// isCommentStart reports whether source[i:] begins with the "--" comment prefix.
func isCommentStart(source []byte, i, n int) bool {
	return i+1 < n && source[i] == '-' && source[i+1] == '-'
}

// skipComment consumes a line or block comment starting at source[i] (which
// must satisfy isCommentStart) and returns the index following it. A
// trailing newline of a line comment is preserved in out.
func skipComment(source []byte, i, n int, out *bytes.Buffer) int {
	i += commentPrefixLen

	if i < n && source[i] == '[' {
		if next, ok := skipBlockComment(source, i, n); ok {
			return next
		}
	}

	// Line comment, skip to end of line.
	for i < n && source[i] != '\n' {
		i++
	}

	// Keep the newline.
	if i < n && source[i] == '\n' {
		out.WriteByte('\n')

		i++
	}

	return i
}

// skipBlockComment attempts to parse a long-bracket block comment starting
// at source[i] == '['. It returns the index following the comment and true
// on success; otherwise it returns false so the caller falls back to
// treating the "--" as a line comment.
func skipBlockComment(source []byte, i, n int) (int, bool) {
	// Count equals signs.
	equals := 0
	j := i + 1

	for j < n && source[j] == '=' {
		equals++
		j++
	}

	if j >= n || source[j] != '[' {
		return 0, false
	}

	// Block comment start: --[=...[; build the matching close pattern.
	closePattern := make([]byte, equals+closeBracketExtra)
	closePattern[0] = ']'

	for k := range equals {
		closePattern[1+k] = '='
	}

	closePattern[equals+1] = ']'

	// Search for close pattern.
	pos := bytes.Index(source[j+1:], closePattern)
	if pos == -1 {
		// Unclosed block comment: the remainder of the source is a comment.
		return n, true
	}

	return j + 1 + pos + len(closePattern), true
}

// copyString copies a quoted string literal starting at source[i], honoring
// backslash escapes, and returns the index following the closing quote.
func copyString(source []byte, i, n int, out *bytes.Buffer) int {
	quote := source[i]
	out.WriteByte(quote)

	i++

	for i < n {
		if source[i] == '\\' && i+1 < n {
			// Escape sequence, copy both characters.
			out.WriteByte(source[i])
			i++

			out.WriteByte(source[i])
			i++

			continue
		}

		if source[i] == quote {
			out.WriteByte(quote)

			i++

			break
		}

		out.WriteByte(source[i])
		i++
	}

	return i
}

// isLongBracketStart reports whether source[i:] begins a long bracket "[[".
func isLongBracketStart(source []byte, i, n int) bool {
	return i+1 < n && source[i] == '[' && source[i+1] == '['
}

// copyLongBracket copies a long string [[ ... ]] (with support for nested
// long brackets) starting at source[i], and returns the index following it.
func copyLongBracket(source []byte, i, n int, out *bytes.Buffer) int {
	out.WriteByte('[')
	out.WriteByte('[')

	i += 2
	depth := 0

	for i < n {
		if i+1 < n && source[i] == ']' && source[i+1] == ']' && depth == 0 {
			out.WriteByte(']')
			out.WriteByte(']')

			i += 2

			break
		}

		if i+1 < n && source[i] == '[' && source[i+1] == '[' {
			depth++
		} else if i+1 < n && source[i] == ']' && source[i+1] == ']' && depth > 0 {
			depth--
		}

		out.WriteByte(source[i])
		i++
	}

	return i
}
