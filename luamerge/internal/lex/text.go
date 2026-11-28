package lex

import (
	"bytes"
	"unicode/utf8"
)

// BOM is the UTF-8 byte order mark.
const BOM = "\xEF\xBB\xBF"

// Header returns the length of the header that the interpreter skips when it
// loads a file (luaL_loadfile): a UTF-8 byte order mark, and a first line
// that starts with "#", such as "#!/usr/bin/env lua". The line break that
// ends that line is not part of the header, so a lexer that starts after the
// header on line 1, as NewFile does, numbers the lines that follow as the
// interpreter does: it compiles the skipped line as an empty one.
//
// The line ends at the first "\n", as in PUC Lua. LuaJIT and Tarantool also
// end it at a lone "\r", which only makes a difference for a file with "\r"
// line endings. Lua 5.1 does not skip a byte order mark.
func Header(src []byte) int {
	n := 0
	if bytes.HasPrefix(src, []byte(BOM)) {
		n = len(BOM)
	}

	if n >= len(src) || src[n] != '#' {
		return n
	}

	end := bytes.IndexByte(src[n:], '\n')
	if end < 0 {
		return len(src)
	}

	return n + end
}

// LineBreak returns the length of the line break at src[i]: 2 for "\r\n" and
// "\n\r", 1 for any other "\n" or "\r", 0 when there is none (i may be
// len(src)). Lua counts each as one line.
func LineBreak(src []byte, i int) int {
	if i >= len(src) || !isLineBreakByte(src[i]) {
		return 0
	}

	if i+1 < len(src) && isLineBreakByte(src[i+1]) && src[i+1] != src[i] {
		return len("\r\n")
	}

	return 1
}

// IsSpace reports whether c is white space to Lua: " ", "\t", "\f", "\v",
// "\n" or "\r".
func IsSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\f' || c == '\v' || isLineBreakByte(c)
}

// NeedsSeparator reports whether the tokens left and right, written with
// nothing between them, would read as something other than these two tokens,
// so they must keep white space between them: "local" and "b" read as
// "localb", "-" and "-" as a comment, "1" and ".." as the malformed numeral
// "1..", "[" and "[" as a long string. Tokens that are separate in the source
// they were read from need no separator where that source had none.
//
// Where a token ends depends on the one byte after it and no further, except
// for "[" before "=", which reads as an unfinished long bracket and so is
// always kept apart: when the first byte of right does not extend left,
// nothing after it can. Which bytes extend a token is the lexer's rule: an
// identifier character a name, an identifier character, a "." and a sign
// after an exponent letter a numeral, the second character of a symbol of
// two ("==", "~=", "<=", ">=", "<<", ">>", "//", "::", "..", "--") or three
// ("...") the first one, and a digit a ".". A string, a long bracket and any
// other symbol end where they end.
func NeedsSeparator(left, right []byte) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}

	next := right[0]

	switch first := left[0]; {
	case isIdentStart(first):
		return isIdent(next)
	case isDigit(first) || first == '.' && len(left) > 1 && isDigit(left[1]):
		hex := len(left) > 1 && first == '0' && (left[1] == 'x' || left[1] == 'X')

		return isIdent(next) || next == '.' || (next == '+' || next == '-') && isExponent(left[len(left)-1], hex)
	case len(left) == 1:
		return extendsSymbol(first, next)
	default:
		return string(left) == ".." && next == '.'
	}
}

// extendsSymbol reports whether next, after the one-character symbol first,
// makes a longer token of them: a symbol of two characters, a comment ("--"),
// a long bracket or an unfinished one ("[[", "[="), or a numeral (".5").
func extendsSymbol(first, next byte) bool {
	switch first {
	case '=', '~':
		return next == '='
	case '<', '>':
		return next == '=' || next == first
	case '/', ':', '-':
		return next == first
	case '[':
		return next == '[' || next == '='
	case '.':
		return next == '.' || isDigit(next)
	default:
		return false
	}
}

// isLineBreakByte reports whether c is "\n" or "\r".
func isLineBreakByte(c byte) bool {
	return c == '\n' || c == '\r'
}

// isDigit reports whether c is a decimal digit.
func isDigit(c byte) bool {
	return '0' <= c && c <= '9'
}

// isIdentStart reports whether c may start a name: a letter, "_", or a byte
// of a multi-byte UTF-8 sequence.
func isIdentStart(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '_' || c >= utf8.RuneSelf
}

// isIdent reports whether c may continue a name.
func isIdent(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}
