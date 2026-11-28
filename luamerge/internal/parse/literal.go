package parse

import (
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

const (
	// hexEscapeLen is the number of hexadecimal digits of a "\x" escape.
	hexEscapeLen = 2
	// decimalEscapeLen is the most decimal digits a "\ddd" escape takes.
	decimalEscapeLen = 3
	// byteMask keeps the low byte of a decimal escape.
	byteMask = 0xFF
)

// stringValue returns the value of the string literal lit, the source of a
// String or LongString token, as LuaJIT and Tarantool decode it: "\x61x" is
// "ax", where Lua 5.1 takes an unknown escape for the character after the
// backslash and reads "x61x". An escape that LuaJIT does not know, which only
// Lua 5.1 accepts, is read the way Lua 5.1 reads it.
func stringValue(lit []byte, kind lex.Kind) []byte {
	if kind == lex.LongString {
		return longStringValue(lit)
	}

	return shortStringValue(lit[1 : len(lit)-1])
}

// longStringValue returns the value of the long string lit, "[==[...]==]":
// its contents without a line break right after the opening bracket, every
// line break read as "\n".
func longStringValue(lit []byte) []byte {
	level := 0
	for lit[level+1] == '=' {
		level++
	}

	brackets := level + len("[[")
	body := lit[brackets : len(lit)-brackets]
	body = body[lex.LineBreak(body, 0):]

	value := make([]byte, 0, len(body))

	for i := 0; i < len(body); {
		if n := lex.LineBreak(body, i); n > 0 {
			value = append(value, '\n')
			i += n

			continue
		}

		value = append(value, body[i])
		i++
	}

	return value
}

// shortStringValue decodes body, the contents of a short string between its
// quotes.
func shortStringValue(body []byte) []byte {
	value := make([]byte, 0, len(body))

	for i := 0; i < len(body); {
		if body[i] != '\\' || i+1 == len(body) {
			value = append(value, body[i])
			i++

			continue
		}

		var n int

		value, n = appendEscape(value, body[i+1:])
		i += 1 + n
	}

	return value
}

// singleCharEscape returns the byte that the escape of the one character char
// after a backslash stands for, such as "\n"; ok is false when char starts no
// such escape.
func singleCharEscape(char byte) (byte, bool) {
	switch char {
	case 'a':
		return '\a', true
	case 'b':
		return '\b', true
	case 'f':
		return '\f', true
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	case 'v':
		return '\v', true
	case '\\', '"', '\'':
		return char, true
	default:
		return 0, false
	}
}

// appendEscape decodes the escape sequence at the start of rest, which follows
// a backslash and is not empty, appends its value to value and returns the
// result and the length of the sequence in rest.
func appendEscape(value, rest []byte) ([]byte, int) {
	if b, ok := singleCharEscape(rest[0]); ok {
		return append(value, b), 1
	}

	if n := lex.LineBreak(rest, 0); n > 0 {
		// A backslash before a line break escapes the whole line break.
		return append(value, '\n'), n
	}

	switch {
	case rest[0] == 'x' && len(rest) > hexEscapeLen && isHex(rest[1]) && isHex(rest[2]):
		b, _ := strconv.ParseUint(string(rest[1:1+hexEscapeLen]), 16, 8)

		return append(value, byte(b)), 1 + hexEscapeLen
	case rest[0] == 'z':
		// "\z" skips the white space after it, line breaks included.
		n := 1
		for n < len(rest) && lex.IsSpace(rest[n]) {
			n++
		}

		return value, n
	case rest[0] == 'u':
		if code, n, ok := utf8Escape(rest); ok {
			return utf8.AppendRune(value, code), n
		}
	case isDecimal(rest[0]):
		n := 1
		for n < decimalEscapeLen && n < len(rest) && isDecimal(rest[n]) {
			n++
		}

		code, _ := strconv.Atoi(string(rest[:n]))

		return append(value, byte(code&byteMask)), n
	}

	// An escape only Lua 5.1 reads: the character after the backslash.
	return append(value, rest[0]), 1
}

// utf8Escape reads the "u{XXX}" of a "\u{XXX}" escape at the start of rest
// and returns the code point and the length of the escape in rest; ok is
// false when rest does not start with a valid one: LuaJIT takes a code point
// of Unicode, with no surrogate, which it encodes in UTF-8.
func utf8Escape(rest []byte) (rune, int, bool) {
	if len(rest) < len("u{}") || rest[1] != '{' {
		return 0, 0, false
	}

	end := 2
	for end < len(rest) && isHex(rest[end]) {
		end++
	}

	if end == 2 || end == len(rest) || rest[end] != '}' {
		return 0, 0, false
	}

	code, err := strconv.ParseUint(string(rest[2:end]), 16, 32)
	if err != nil || code > unicode.MaxRune || !utf8.ValidRune(rune(code)) {
		return 0, 0, false
	}

	return rune(code), end + 1, true
}

// isDecimal reports whether c is a decimal digit.
func isDecimal(c byte) bool {
	return '0' <= c && c <= '9'
}

// isHex reports whether c is a hexadecimal digit.
func isHex(c byte) bool {
	return isDecimal(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
