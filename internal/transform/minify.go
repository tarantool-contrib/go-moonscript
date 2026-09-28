package transform

type minifyTransformer struct{}

func (t *minifyTransformer) Transform(source []byte) ([]byte, error) {
	// Remove shebang line if present.
	source = StripShebang(source)

	// First remove comments.
	ct := &commentTransformer{}

	withoutComments, err := ct.Transform(source)
	if err != nil {
		return nil, err
	}

	// Then remove empty lines.
	elt := &emptyLineTransformer{}

	withoutEmptyLines, err := elt.Transform(withoutComments)
	if err != nil {
		return nil, err
	}

	// Simple whitespace reduction: collapse multiple spaces to single space.
	// This is a basic implementation; a proper minifier would do more.
	result := reduceWhitespace(withoutEmptyLines)

	return result, nil
}

func reduceWhitespace(source []byte) []byte {
	var out []byte

	i := 0
	n := len(source)
	inString := false
	stringChar := byte(0)

	for i < n {
		char := source[i]

		// Track string state.
		if !inString && (char == '"' || char == '\'') {
			inString = true
			stringChar = char
			out = append(out, char)
			i++

			continue
		}

		if inString && char == stringChar {
			// An escaped quote keeps us inside the string.
			if i == 0 || source[i-1] != '\\' {
				inString = false
			}

			out = append(out, char)
			i++

			continue
		}

		if !inString && isLongBracketStart(source, i, n) {
			// Long string start.
			out, i = copyLongBracketBytes(source, out, i, n)

			continue
		}

		if !inString && (char == ' ' || char == '\t') {
			out, i = collapseSpaces(source, out, i, n)

			continue
		}

		// Remove trailing whitespace at end of line.
		if !inString && char == '\n' {
			out = trimTrailingWhitespace(out)
		}

		out = append(out, char)
		i++
	}

	return out
}

// isLongBracketStart reports whether source[i:] begins a long bracket "[[".
func isLongBracketStart(source []byte, i, n int) bool {
	return i+1 < n && source[i] == '[' && source[i+1] == '['
}

// copyLongBracketBytes copies a long string [[ ... ]] (with support for
// nested long brackets) starting at source[i] into out, and returns the
// updated out slice and the index following the copied bracket.
func copyLongBracketBytes(source []byte, out []byte, i, n int) ([]byte, int) {
	out = append(out, source[i], source[i+1])
	i += 2
	depth := 0

	for i < n {
		if i+1 < n && source[i] == ']' && source[i+1] == ']' && depth == 0 {
			out = append(out, ']', ']')
			i += 2

			break
		}

		if i+1 < n && source[i] == '[' && source[i+1] == '[' {
			depth++
		} else if i+1 < n && source[i] == ']' && source[i+1] == ']' && depth > 0 {
			depth--
		}

		out = append(out, source[i])
		i++
	}

	return out, i
}

// collapseSpaces collapses the run of spaces/tabs starting at source[i] into
// a single space appended to out, and returns the updated out slice and the
// index following the run.
func collapseSpaces(source []byte, out []byte, i, n int) ([]byte, int) {
	out = append(out, ' ')

	// Skip all consecutive whitespace.
	for i < n && (source[i] == ' ' || source[i] == '\t') {
		i++
	}

	return out, i
}

// trimTrailingWhitespace removes trailing spaces/tabs from the end of out.
func trimTrailingWhitespace(out []byte) []byte {
	for len(out) > 0 && (out[len(out)-1] == ' ' || out[len(out)-1] == '\t') {
		out = out[:len(out)-1]
	}

	return out
}
