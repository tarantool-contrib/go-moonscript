package parse

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

const (
	// hexEscapeLen is the number of hexadecimal digits of a "\x" escape.
	hexEscapeLen = 2
	// decimalEscapeLen is the most decimal digits a "\ddd" escape takes.
	decimalEscapeLen = 3
	// maxUTF8Escape is the largest code point a "\u{...}" escape takes: Lua
	// 5.4 and later encode up to 2^31 - 1 in up to six bytes.
	maxUTF8Escape = 0x7FFFFFFF
	// asciiLimit is the first code point that UTF-8 encodes in more than one
	// byte.
	asciiLimit = 0x80
	// utf8ContinuationBits is the number of payload bits of a UTF-8
	// continuation byte.
	utf8ContinuationBits = 6
	// utf8ContinuationMask selects them; utf8ContinuationTag marks the byte.
	utf8ContinuationMask = 0x3F
	utf8ContinuationTag  = 0x80
	// utf8FirstByteMax is the largest payload the first byte of a sequence of
	// two bytes holds; each continuation byte halves it for the first byte.
	utf8FirstByteMax = 0x3F
	// byteMask keeps the low byte of a decimal escape.
	byteMask = 0xFF
)

// stringValue returns the value of the string literal lit, the source of a
// String or LongString token, as Lua 5.3 and later decode it. LuaJIT and
// Tarantool decode every valid literal the same way, and so do Lua 5.1 and 5.2
// but for the escapes they do not have: "\x61x" is "ax", while Lua 5.1 takes
// an unknown escape for the character after the backslash and reads "x61x".
// Such an escape, which only Lua 5.1 accepts, is read the way Lua 5.1 reads
// it.
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
			return appendUTF8(value, code), n
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
// false when rest does not start with a valid one.
func utf8Escape(rest []byte) (uint64, int, bool) {
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

	code, err := strconv.ParseUint(string(rest[2:end]), 16, 64)
	if err != nil || code > maxUTF8Escape {
		return 0, 0, false
	}

	return code, end + 1, true
}

// appendUTF8 appends the UTF-8 encoding of code, a code point up to 2^31 - 1,
// the way Lua encodes a "\u{XXX}" escape (luaO_utf8esc): surrogates are
// encoded like any other code point, and code points beyond Unicode take up
// to six bytes.
func appendUTF8(value []byte, code uint64) []byte {
	if code < asciiLimit {
		return append(value, byte(code))
	}

	var continuation []byte

	firstMax := uint64(utf8FirstByteMax)

	for {
		continuation = append(continuation, byte(code&utf8ContinuationMask)|utf8ContinuationTag)
		code >>= utf8ContinuationBits
		firstMax >>= 1

		if code <= firstMax {
			break
		}
	}

	// The first byte holds the rest of code, which is at most firstMax, after
	// as many 1 bits as the sequence has bytes and a 0 bit. firstMax is below
	// utf8FirstByteMax by now, so the mask takes nothing away.
	value = append(value, byte(^firstMax<<1&byteMask)|byte(code&utf8FirstByteMax))

	for _, b := range slices.Backward(continuation) {
		value = append(value, b)
	}

	return value
}

// quote returns a Lua 5.1 string literal of value that every Lua reads as
// value: printable ASCII as is, every other byte as a three-digit decimal
// escape, so no digit after an escape is taken into it.
func quote(value []byte) []byte {
	lit := make([]byte, 0, len(value)+len(`""`))
	lit = append(lit, '"')

	for _, char := range value {
		if char >= ' ' && char <= '~' && char != '"' && char != '\\' {
			lit = append(lit, char)

			continue
		}

		lit = fmt.Appendf(lit, `\%03d`, char)
	}

	return append(lit, '"')
}

// isDecimal reports whether c is a decimal digit.
func isDecimal(c byte) bool {
	return '0' <= c && c <= '9'
}

// isHex reports whether c is a hexadecimal digit.
func isHex(c byte) bool {
	return isDecimal(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
