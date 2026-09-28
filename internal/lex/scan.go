package lex

// bracketLen is the number of brackets in a long bracket's opener or closer:
// "[" and "[" around the "=" signs of "[==[".
const bracketLen = 2

// scan reads the token that starts at the current position, which is not
// white space, and returns its kind.
func (l *Lexer) scan() (Kind, error) {
	char := l.src[l.pos]

	switch {
	case char == '-':
		return l.minus()
	case char == '[':
		return l.bracket()
	case char == '"' || char == '\'':
		return String, l.shortString(char)
	case char == '.':
		return l.dot(), nil
	case isDigit(char):
		l.number()

		return Number, nil
	case isIdentStart(char):
		for l.pos < len(l.src) && isIdent(l.src[l.pos]) {
			l.pos++
		}

		return Name, nil
	default:
		return l.symbol(), nil
	}
}

// at reports whether the byte at offset ahead from the current position is c.
func (l *Lexer) at(ahead int, c byte) bool {
	return l.pos+ahead < len(l.src) && l.src[l.pos+ahead] == c
}

// minus reads "-" or a comment.
func (l *Lexer) minus() (Kind, error) {
	if !l.at(1, '-') {
		l.pos++

		return Symbol, nil
	}

	l.pos += len("--")

	if l.at(0, '[') {
		level, ok := l.level(l.pos)
		if ok {
			l.pos += level + bracketLen

			return LongComment, l.longBody(level, ErrUnfinishedLongComment)
		}
	}

	// A short comment, even after an opener that is not one, such as "--[=".
	for l.pos < len(l.src) && !isLineBreakByte(l.src[l.pos]) {
		l.pos++
	}

	return Comment, nil
}

// bracket reads "[" or a long string.
func (l *Lexer) bracket() (Kind, error) {
	level, ok := l.level(l.pos)

	switch {
	case ok:
		l.pos += level + bracketLen

		return LongString, l.longBody(level, ErrUnfinishedLongString)
	case level > 0:
		return Invalid, ErrLongBracket
	default:
		l.pos++

		return Symbol, nil
	}
}

// level counts the "=" signs after the bracket at offset i and reports
// whether the same bracket follows them: "[==[" gives 2 and true, "[==x" 2
// and false, and "]]" 0 and true.
func (l *Lexer) level(i int) (int, bool) {
	j := i + 1
	for j < len(l.src) && l.src[j] == '=' {
		j++
	}

	return j - i - 1, j < len(l.src) && l.src[j] == l.src[i]
}

// longBody reads the rest of a long string or comment of the given level,
// whose opener has been read, up to and including the first closer of that
// level. unfinished is the error when the source ends first.
func (l *Lexer) longBody(level int, unfinished error) error {
	for l.pos < len(l.src) {
		switch {
		case isLineBreakByte(l.src[l.pos]):
			l.newline()
		case l.src[l.pos] == ']':
			closer, ok := l.level(l.pos)
			if ok && closer == level {
				l.pos += level + bracketLen

				return nil
			}

			l.pos++
		default:
			l.pos++
		}
	}

	return unfinished
}

// shortString reads a string delimited by quote.
func (l *Lexer) shortString(quote byte) error {
	l.pos++

	for l.pos < len(l.src) {
		char := l.src[l.pos]

		switch {
		case char == quote:
			l.pos++

			return nil
		case isLineBreakByte(char):
			return ErrUnfinishedString
		case char == '\\':
			l.pos++
			if l.pos >= len(l.src) {
				return ErrUnfinishedString
			}

			l.escape()
		default:
			l.pos++
		}
	}

	return ErrUnfinishedString
}

// escape moves past the escape sequence whose backslash has been read. Only
// two escapes need more than their next byte to find the end of the string: a
// backslash before a line break escapes the whole line break ("\r\n" is one),
// and "\z" skips the white space after it, line breaks included. Any other
// escape is taken as its next byte alone. The rest of a well-formed \x41,
// \u{48} or \065 cannot end the string anyway; after a malformed one, such as
// "\x" right before the closing quote, the string ends where Lua 5.1 ends it
// (Lua 5.2 and later reject the escape).
func (l *Lexer) escape() {
	switch {
	case isLineBreakByte(l.src[l.pos]):
		l.newline()
	case l.src[l.pos] == 'z':
		l.pos++
		l.skipSpace()
	default:
		l.pos++
	}
}

// dot reads ".", "..", "..." or a numeral that starts with ".".
func (l *Lexer) dot() Kind {
	switch {
	case l.at(1, '.'):
		l.pos += len("..")
		if l.at(0, '.') {
			l.pos++
		}
	case l.pos+1 < len(l.src) && isDigit(l.src[l.pos+1]):
		l.number()

		return Number
	default:
		l.pos++
	}

	return Symbol
}

// number reads a numeral the way LuaJIT does: every identifier character,
// every ".", and a sign right after an exponent letter, which is e or E, or p
// or P after 0x.
func (l *Lexer) number() {
	hex := l.at(0, '0') && (l.at(1, 'x') || l.at(1, 'X'))
	prev := byte(0)

	for l.pos < len(l.src) {
		char := l.src[l.pos]

		sign := (char == '+' || char == '-') && isExponent(prev, hex)
		if !isIdent(char) && char != '.' && !sign {
			return
		}

		prev = char
		l.pos++
	}
}

// isExponent reports whether c is an exponent letter of a decimal (hex false)
// or hexadecimal numeral.
func isExponent(c byte, hex bool) bool {
	if hex {
		return c == 'p' || c == 'P'
	}

	return c == 'e' || c == 'E'
}

// symbol reads an operator or punctuation other than those starting with
// "-", "[" or ".", or a byte that starts no token.
func (l *Lexer) symbol() Kind {
	switch l.src[l.pos] {
	case '=', '~': // == ~=
		l.pos += l.width('=')
	case '<': // <= <<
		l.pos += l.width('=', '<')
	case '>': // >= >>
		l.pos += l.width('=', '>')
	case '/': // //
		l.pos += l.width('/')
	case ':': // ::
		l.pos += l.width(':')
	case '+', '*', '%', '^', '#', '&', '|', '(', ')', '{', '}', ']', ';', ',':
		l.pos++
	default:
		l.pos++

		return Invalid
	}

	return Symbol
}

// width is the length of the symbol at the current position: 2 when one of
// seconds follows its first byte, 1 otherwise.
func (l *Lexer) width(seconds ...byte) int {
	for _, c := range seconds {
		if l.at(1, c) {
			return len("==")
		}
	}

	return 1
}
