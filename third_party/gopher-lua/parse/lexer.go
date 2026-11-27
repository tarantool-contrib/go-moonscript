package parse

// go-moonscript patch: the lexer is Lua 5.1's llex.c (Lua51) or LuaJIT
// 2.1's lj_lex.c (LuaJIT) ported to Go, so that the same sources are
// accepted and errors read the same: "<chunk>:<line>: <message> near
// '<token>'", the token as luaX_lexerror/lj_lex_error print it. It replaces
// gopher-lua's scanner.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
)

// EOF is the end of the input.
const EOF = -1

// Dialect is the language the parser accepts.
type Dialect int

const (
	// Lua51 is PUC Lua 5.1.5's syntax.
	Lua51 Dialect = iota
	// LuaJIT is LuaJIT 2.1's (without LUAJIT_ENABLE_LUA52COMPAT): goto and
	// labels, hexadecimal floats and binary numerals, \x \z \u{} escapes,
	// identifiers with non-ASCII bytes, numerals as lj_strscan reads them.
	// Its FFI numerals (1LL, 1ULL, 1i) are accepted, as LuaJIT with the FFI
	// does, but are cdata, which the VM doesn't have: they are
	// ast.CdataNumberExpr, which evaluate to an error.
	LuaJIT
)

// Error is a syntax error. Pos.Line is the line Lua reports it on; Token is
// the token it is reported near as Lua prints it ("<eof>" at the end of the
// input), or "" for an error reported near no token (a limit).
type Error struct {
	Pos     ast.Position
	Message string
	Token   string
}

// Error formats the error as Lua does, with the chunk name verbatim:
// "<chunk>:<line>: <message> near '<token>'".
func (e *Error) Error() string {
	if e.Token == "" {
		return fmt.Sprintf("%s:%d: %s", e.Pos.Source, e.Pos.Line, e.Message)
	}
	return fmt.Sprintf("%s:%d: %s near '%s'", e.Pos.Source, e.Pos.Line, e.Message, e.Token)
}

// Tokens: a single character is its own code.
const (
	tkAnd = 257 + iota
	tkBreak
	tkDo
	tkElse
	tkElseif
	tkEnd
	tkFalse
	tkFor
	tkFunction
	tkGoto
	tkIf
	tkIn
	tkLocal
	tkNil
	tkNot
	tkOr
	tkRepeat
	tkReturn
	tkThen
	tkTrue
	tkUntil
	tkWhile
	tkConcat
	tkDots
	tkEq
	tkGe
	tkLe
	tkNe
	tkLabel
	tkNumber
	tkName
	tkString
	tkEOS
)

const firstReserved = tkAnd

var tokenNames = [...]string{
	"and", "break", "do", "else", "elseif", "end", "false", "for", "function",
	"goto", "if", "in", "local", "nil", "not", "or", "repeat", "return", "then",
	"true", "until", "while", "..", "...", "==", ">=", "<=", "~=", "::",
	"<number>", "<name>", "<string>", "<eof>",
}

var reserved = func() map[string]int {
	m := map[string]int{}
	for i := tkAnd; i <= tkWhile; i++ {
		m[tokenNames[i-firstReserved]] = i
	}
	return m
}()

// token is a token, its semantic value (a name, the value of a string, the
// text of a number) and the line it starts on.
type token struct {
	typ   int
	sem   string
	line  int
	cdata bool // a LuaJIT FFI numeral (1LL, 1ULL, 1i), sem its text
}

type lexer struct {
	src        []byte
	pos        int // of the character after current
	current    int // the character looked at, EOF at the end
	linenumber int
	lastline   int // linenumber when the current token was taken
	buff       []byte
	source     string
	dialect    Dialect
	t          token // the current token
	ahead      token // the look-ahead token, typ tkEOS if none
	prevLine   int   // the line of the token before t
}

func newLexer(src []byte, source string, dialect Dialect) *lexer {
	ls := &lexer{src: src, linenumber: 1, lastline: 1, source: source, dialect: dialect}
	ls.ahead.typ = tkEOS
	ls.next()
	if dialect == LuaJIT { // lj_lex_setup skips a UTF-8 BOM and a "#" line
		if ls.current == 0xef && len(ls.src) >= ls.pos+2 && ls.src[ls.pos] == 0xbb && ls.src[ls.pos+1] == 0xbf {
			ls.pos += 2
			ls.next()
		}
		if ls.current == '#' {
			for {
				ls.next()
				if ls.current == EOF {
					return ls
				}
				if ls.currIsNewline() {
					break
				}
			}
			ls.inclinenumber()
		}
	}
	return ls
}

/* character classes: the C locale's (Lua 5.1), lj_char's (LuaJIT) */

func isDigit(c int) bool  { return '0' <= c && c <= '9' }
func isXDigit(c int) bool { return isDigit(c) || 'a' <= c|0x20 && c|0x20 <= 'f' }
func isAlpha(c int) bool  { return 'a' <= c|0x20 && c|0x20 <= 'z' }
func isAlnum(c int) bool  { return isAlpha(c) || isDigit(c) }
func isSpace(c int) bool  { return c == ' ' || '\t' <= c && c <= '\r' }
func isCntrl(c int) bool  { return 0 <= c && c < 32 || c == 127 }

// isIdent is lj_char_isident: alphanumeric, '_' and the bytes 128-255.
func isIdent(c int) bool { return isAlnum(c) || c == '_' || c >= 128 }

func (ls *lexer) next() {
	if ls.pos < len(ls.src) {
		ls.current = int(ls.src[ls.pos])
		ls.pos++
	} else {
		ls.current = EOF
	}
}

func (ls *lexer) save(c int) { ls.buff = append(ls.buff, byte(c)) }

func (ls *lexer) saveNext() {
	ls.save(ls.current)
	ls.next()
}

func (ls *lexer) currIsNewline() bool { return ls.current == '\n' || ls.current == '\r' }

func (ls *lexer) inclinenumber() {
	old := ls.current
	ls.next() // skip '\n' or '\r'
	if ls.currIsNewline() && ls.current != old {
		ls.next() // skip '\n\r' or '\r\n'
	}
	ls.linenumber++
}

func (ls *lexer) checkNext(set string) bool {
	if ls.current < 0 || !strings.ContainsRune(set, rune(ls.current)) {
		return false
	}
	ls.saveNext()
	return true
}

/* errors */

func (ls *lexer) token2str(tok int) string {
	if tok < firstReserved {
		if isCntrl(tok) {
			return fmt.Sprintf("char(%d)", tok)
		}
		return string([]byte{byte(tok)})
	}
	return tokenNames[tok-firstReserved]
}

// txtToken is what an error near tok shows: the text of the token being
// read for a name, string or number (up to a NUL, as a C string).
func (ls *lexer) txtToken(tok int) string {
	switch tok {
	case tkName, tkString, tkNumber:
		b := ls.buff
		for i, c := range b {
			if c == 0 {
				b = b[:i]
				break
			}
		}
		return string(b)
	}
	return ls.token2str(tok)
}

// errorAt panics with a syntax error on line, near tok (no token if 0).
func (ls *lexer) errorAt(line int, msg string, tok int) {
	near := ""
	if tok != 0 {
		near = ls.txtToken(tok)
	}
	panic(&Error{Pos: ast.Position{Source: ls.source, Line: line}, Message: msg, Token: near})
}

// lexError is luaX_lexerror / lj_lex_error.
func (ls *lexer) lexError(msg string, tok int) { ls.errorAt(ls.linenumber, msg, tok) }

// syntaxError is luaX_syntaxerror: an error near the current token.
func (ls *lexer) syntaxError(msg string) { ls.lexError(msg, ls.t.typ) }

/* scanning */

// readNumeral is Lua 5.1's read_numeral.
func (ls *lexer) readNumeral() string {
	for {
		ls.saveNext()
		if !isDigit(ls.current) && ls.current != '.' {
			break
		}
	}
	if ls.checkNext("Ee") {
		ls.checkNext("+-")
	}
	for isAlnum(ls.current) || ls.current == '_' {
		ls.saveNext()
	}
	s := string(ls.buff)
	if !isNumeral(s) {
		ls.lexError("malformed number", tkNumber)
	}
	return s
}

// isNumeral reports whether s, a numeral as read_numeral reads it, is a
// number for luaO_str2d (strtod): decimal with an optional exponent, or
// "0x" and hexadecimal digits with an optional binary exponent.
func isNumeral(s string) bool {
	digit, expch, i := isDigit, byte('e'), 0
	if len(s) > 2 && s[0] == '0' && s[1]|0x20 == 'x' {
		digit, expch, i = isXDigit, 'p', 2
	}
	ndigits := 0
	for ; i < len(s) && digit(int(s[i])); i++ {
		ndigits++
	}
	if i < len(s) && s[i] == '.' {
		for i++; i < len(s) && digit(int(s[i])); i++ {
			ndigits++
		}
	}
	if ndigits == 0 {
		return false
	}
	if i < len(s) && s[i]|0x20 == expch {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i == len(s) || !isDigit(int(s[i])) {
			return false
		}
		for i < len(s) && isDigit(int(s[i])) {
			i++
		}
	}
	return i == len(s)
}

// lexNumber is LuaJIT's lex_number: the numeral, as lj_strscan reads it with
// the FFI (scanNumeral). It returns the token's value, the number formatted
// to convert back exactly (by parseNumber) or the numeral of a cdata one
// (1LL, 1ULL, 1i), and whether the numeral is cdata.
func (ls *lexer) lexNumber() (string, bool) {
	xp := 'e'
	c := ls.current
	if c == '0' {
		ls.saveNext()
		if ls.current|0x20 == 'x' {
			xp = 'p'
		}
	}
	for isIdent(ls.current) || ls.current == '.' ||
		(ls.current == '-' || ls.current == '+') && rune(c|0x20) == xp {
		c = ls.current
		ls.saveNext()
	}
	s := string(ls.buff)
	v, cdata, ok := scanNumeral(s)
	if !ok {
		ls.lexError("malformed number", tkNumber)
	}
	if cdata {
		return s, true
	}
	return strconv.FormatFloat(v, 'g', -1, 64), false
}

func (ls *lexer) skipSep() int {
	count := 0
	s := ls.current
	ls.saveNext()
	for ls.current == '=' {
		ls.saveNext()
		count++
	}
	if ls.current == s {
		return count
	}
	return -count - 1
}

// readLongString reads a long string, or a long comment if !isString.
func (ls *lexer) readLongString(isString bool, sep int) string {
	ls.saveNext() // skip the second '['
	if ls.currIsNewline() {
		ls.inclinenumber() // a first newline is skipped
	}
	for {
		switch ls.current {
		case EOF:
			if isString {
				ls.lexError("unfinished long string", tkEOS)
			}
			ls.lexError("unfinished long comment", tkEOS)
		case '[':
			if ls.dialect == LuaJIT {
				ls.saveNext()
				break
			}
			// Lua 5.1's LUA_COMPAT_LSTR 1
			if ls.skipSep() == sep {
				ls.saveNext()
				if sep == 0 {
					ls.lexError("nesting of [[...]] is deprecated", '[')
				}
			}
		case ']':
			if ls.skipSep() == sep {
				ls.saveNext() // skip the second ']'
				if isString {
					return string(ls.buff[2+sep : len(ls.buff)-(2+sep)])
				}
				return ""
			}
		case '\n', '\r':
			ls.save('\n')
			ls.inclinenumber()
			if !isString {
				ls.buff = ls.buff[:0]
			}
		default:
			if isString {
				ls.saveNext()
			} else {
				ls.next()
			}
		}
	}
}

// readString is Lua 5.1's read_string.
func (ls *lexer) readString(del int) string {
	ls.saveNext()
	for ls.current != del {
		switch ls.current {
		case EOF:
			ls.lexError("unfinished string", tkEOS)
		case '\n', '\r':
			ls.lexError("unfinished string", tkString)
		case '\\':
			ls.next() // the '\' is not saved
			c := 0
			switch ls.current {
			case 'a':
				c = '\a'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'v':
				c = '\v'
			case '\n', '\r':
				ls.save('\n')
				ls.inclinenumber()
				continue
			case EOF:
				continue // an error next
			default:
				if !isDigit(ls.current) {
					ls.saveNext() // \\, \", \' and any other: the character
				} else { // \ddd
					for i := 0; i < 3 && isDigit(ls.current); i++ {
						c = 10*c + ls.current - '0'
						ls.next()
					}
					if c > 255 {
						ls.lexError("escape sequence too large", tkString)
					}
					ls.save(c)
				}
				continue
			}
			ls.save(c)
			ls.next()
		default:
			ls.saveNext()
		}
	}
	ls.saveNext() // skip the delimiter
	return string(ls.buff[1 : len(ls.buff)-1])
}

// lexString is LuaJIT's lex_string.
func (ls *lexer) lexString() string {
	del := ls.current
	ls.saveNext()
	for ls.current != del {
		switch ls.current {
		case EOF:
			ls.lexError("unfinished string", tkEOS)
		case '\n', '\r':
			ls.lexError("unfinished string", tkString)
		case '\\':
			ls.next() // skip the '\'
			c := ls.current
			switch c {
			case 'a':
				c = '\a'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'v':
				c = '\v'
			case 'x': // \xXX
				c = 0
				for i := 0; i < 2; i++ {
					ls.next()
					c = c<<4 + ls.hexDigit()
				}
			case 'u': // \u{XXX}
				ls.next()
				if ls.current != '{' {
					ls.lexError("invalid escape sequence", tkString)
				}
				ls.next()
				c = 0
				for {
					c = c<<4 | ls.hexDigit()
					if c >= 0x110000 {
						ls.lexError("invalid escape sequence", tkString)
					}
					if ls.next(); ls.current == '}' {
						break
					}
				}
				if c >= 0xd800 && c < 0xe000 { // no surrogates
					ls.lexError("invalid escape sequence", tkString)
				}
				if c >= 0x80 {
					var b [4]byte
					n := encodeUTF8(b[:], c)
					for _, x := range b[:n-1] {
						ls.save(int(x))
					}
					c = int(b[n-1])
				}
			case 'z': // skip whitespace
				ls.next()
				for isSpace(ls.current) {
					if ls.currIsNewline() {
						ls.inclinenumber()
					} else {
						ls.next()
					}
				}
				continue
			case '\n', '\r':
				ls.save('\n')
				ls.inclinenumber()
				continue
			case '\\', '"', '\'':
			case EOF:
				continue // an error next
			default:
				if !isDigit(c) {
					ls.lexError("invalid escape sequence", tkString)
				}
				c -= '0' // \ddd
				if ls.next(); isDigit(ls.current) {
					c = c*10 + ls.current - '0'
					if ls.next(); isDigit(ls.current) {
						c = c*10 + ls.current - '0'
						if c > 255 {
							ls.lexError("invalid escape sequence", tkString)
						}
						ls.next()
					}
				}
				ls.save(c)
				continue
			}
			ls.save(c)
			ls.next()
		default:
			ls.saveNext()
		}
	}
	ls.saveNext() // skip the delimiter
	return string(ls.buff[1 : len(ls.buff)-1])
}

// hexDigit is the value of the current character, a hexadecimal digit in
// an escape sequence.
func (ls *lexer) hexDigit() int {
	if !isXDigit(ls.current) {
		ls.lexError("invalid escape sequence", tkString)
	}
	if isDigit(ls.current) {
		return ls.current - '0'
	}
	return ls.current&15 + 9
}

// encodeUTF8 encodes c (below 0x110000) as LuaJIT does a \u{} escape.
func encodeUTF8(b []byte, c int) int {
	switch {
	case c < 0x80:
		b[0] = byte(c)
		return 1
	case c < 0x800:
		b[0], b[1] = byte(0xc0|c>>6), byte(0x80|c&0x3f)
		return 2
	case c < 0x10000:
		b[0], b[1], b[2] = byte(0xe0|c>>12), byte(0x80|c>>6&0x3f), byte(0x80|c&0x3f)
		return 3
	}
	b[0], b[1], b[2], b[3] = byte(0xf0|c>>18), byte(0x80|c>>12&0x3f), byte(0x80|c>>6&0x3f), byte(0x80|c&0x3f)
	return 4
}

// llex reads the next token: its type, and its semantic value and start
// line into tok.
func (ls *lexer) llex(tok *token) int {
	ls.buff = ls.buff[:0]
	tok.cdata = false
	jit := ls.dialect == LuaJIT
	for {
		tok.line = ls.linenumber
		switch c := ls.current; c {
		case '\n', '\r':
			ls.inclinenumber()
			continue
		case '-':
			ls.next()
			if ls.current != '-' {
				return '-'
			}
			// a comment
			ls.next()
			if ls.current == '[' {
				sep := ls.skipSep()
				ls.buff = ls.buff[:0]
				if sep >= 0 {
					ls.readLongString(false, sep)
					ls.buff = ls.buff[:0]
					continue
				}
			}
			for !ls.currIsNewline() && ls.current != EOF {
				ls.next()
			}
			continue
		case '[':
			sep := ls.skipSep()
			if sep >= 0 {
				tok.sem = ls.readLongString(true, sep)
				return tkString
			} else if sep == -1 {
				return '['
			}
			ls.lexError("invalid long string delimiter", tkString)
		case '=':
			ls.next()
			if ls.current != '=' {
				return '='
			}
			ls.next()
			return tkEq
		case '<':
			ls.next()
			if ls.current != '=' {
				return '<'
			}
			ls.next()
			return tkLe
		case '>':
			ls.next()
			if ls.current != '=' {
				return '>'
			}
			ls.next()
			return tkGe
		case '~':
			ls.next()
			if ls.current != '=' {
				return '~'
			}
			ls.next()
			return tkNe
		case ':':
			if !jit {
				break
			}
			ls.next()
			if ls.current != ':' {
				return ':'
			}
			ls.next()
			return tkLabel
		case '"', '\'':
			if jit {
				tok.sem = ls.lexString()
			} else {
				tok.sem = ls.readString(c)
			}
			return tkString
		case '.':
			ls.saveNext()
			if ls.checkNext(".") {
				if ls.checkNext(".") {
					return tkDots
				}
				return tkConcat
			} else if !isDigit(ls.current) {
				return '.'
			}
			if jit {
				tok.sem, tok.cdata = ls.lexNumber()
			} else {
				tok.sem = ls.readNumeral()
			}
			return tkNumber
		case EOF:
			return tkEOS
		}
		c := ls.current
		switch {
		case isSpace(c):
			ls.next()
			continue
		case isDigit(c):
			if jit {
				tok.sem, tok.cdata = ls.lexNumber()
			} else {
				tok.sem = ls.readNumeral()
			}
			return tkNumber
		case isAlpha(c) || c == '_' || jit && c >= 128:
			for {
				ls.saveNext()
				if !(isAlnum(ls.current) || ls.current == '_' || jit && ls.current >= 128) {
					break
				}
			}
			tok.sem = string(ls.buff)
			if r, ok := reserved[tok.sem]; ok && (r != tkGoto || jit) {
				return r
			}
			return tkName
		}
		ls.next()
		return c // a single-character token
	}
}

// nextToken is luaX_next.
func (ls *lexer) nextToken() {
	ls.lastline = ls.linenumber
	ls.prevLine = ls.t.line
	if ls.ahead.typ != tkEOS {
		ls.t = ls.ahead
		ls.ahead.typ = tkEOS
	} else {
		ls.t.sem = ""
		ls.t.typ = ls.llex(&ls.t)
	}
}

// lookahead is luaX_lookahead: it reads the token after the current one.
func (ls *lexer) lookahead() int {
	ls.ahead.sem = ""
	ls.ahead.typ = ls.llex(&ls.ahead)
	return ls.ahead.typ
}
