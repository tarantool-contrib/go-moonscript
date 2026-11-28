// Package lex splits Lua source into tokens: the tokens that the lexer of
// the gopher-lua fork of this repository reads in its LuaJIT dialect
// (parse.Tokenize of lua/parse), with every token's exact byte span and
// lines, so a tool can rebuild source from the original bytes: keep the
// tokens it wants byte for byte and decide what goes between them.
//
// luamerge reads files written for Lua 5.1, LuaJIT and Tarantool, and where
// the dialects differ the tokens are those of the union of them, so that a
// source one of them accepts is never refused:
//
//   - The lexer of LuaJIT reads most of the union: long brackets of any
//     level, which end at the first closing bracket of their level (no
//     supported dialect counts nesting, and Lua 5.1 refuses a "[[" inside a
//     level-0 one, so valid source never has one), the string escapes "\x",
//     "\z" and "\u{...}" and a "\" before a line break, hexadecimal floats,
//     LuaJIT's 1LL, 0x10ULL, 2i and 0b101, names with bytes 0x80 to 0xFF,
//     "::", and the line breaks "\n", "\r", "\r\n" and "\n\r", each one line.
//   - Where it stops at a lexical error that Lua 5.1 does not make, the
//     token is read whole as Lua 5.1 reads it and LuaJIT's lexer reads on
//     after it: a short string with an escape that only Lua 5.1 reads (an
//     unknown one, "\q", read as the character after the backslash), and a
//     numeral that Lua 5.1 ends before ".." (1e5..x is 1e5 .. x there): it is
//     scanned the way LuaJIT scans a numeral, every identifier character,
//     every "." and a sign after an exponent letter, and is one token.
//     Escapes are not decoded and numerals not validated here.
//
// The union is also the most cautious answer to where tokens meet: two
// tokens read as separate are separate in every dialect.
//
// Like Lua's own lexer this one leaves grammar to a parser: a byte that
// starts no token is a token of kind Invalid, and which names are reserved
// words is not its business. goto is a name in some LuaJIT builds. What it
// does refuse is what no dialect can read: a string, long string or long
// comment without its end, and "[" followed by "=" signs but no second "[".
package lex

import (
	"errors"
	"fmt"
)

// Kind is the kind of a token.
type Kind uint8

const (
	// EOF is the end of the source. Its span is empty.
	EOF Kind = iota
	// Name is an identifier or a reserved word.
	Name
	// Number is a numeral.
	Number
	// String is a short string, quotes included: "..." or '...'.
	String
	// LongString is a long bracket string, brackets included: [[...]],
	// [==[...]==].
	LongString
	// Comment is a short comment: "--" and the rest of its line, without
	// the line break that ends it.
	Comment
	// LongComment is a long comment: --[[...]], --[==[...]==].
	LongComment
	// Symbol is an operator or punctuation: + - * / % ^ # == ~= <= >= < > =
	// ( ) { } [ ] :: ; : , . .. ...
	Symbol
	// Invalid is a byte that starts no token in any dialect, such as "$" or
	// "!". Lua reads it as a token of its own and its parser rejects it.
	Invalid
)

// String returns the name of the kind.
func (k Kind) String() string {
	switch k {
	case EOF:
		return "EOF"
	case Name:
		return "Name"
	case Number:
		return "Number"
	case String:
		return "String"
	case LongString:
		return "LongString"
	case Comment:
		return "Comment"
	case LongComment:
		return "LongComment"
	case Symbol:
		return "Symbol"
	case Invalid:
		return "Invalid"
	default:
		return fmt.Sprintf("Kind(%d)", uint8(k))
	}
}

// Token is one token of the source.
type Token struct {
	// Kind is what the token is.
	Kind Kind
	// Start and End delimit the token's bytes: src[Start:End].
	Start, End int
	// Line is the line of the token's first byte and EndLine the line of its
	// last one; they differ when the token contains a line break: a long
	// string or comment, or a short string with an escaped line break or a
	// "\z". Lines count from 1 as Lua counts them.
	Line, EndLine int
}

// Sentinel errors for the constructs no dialect can read; errors.Is tells
// them apart.
var (
	// ErrUnfinishedString is a short string without its closing quote.
	ErrUnfinishedString = errors.New("unfinished string")
	// ErrUnfinishedLongString is a long string without its closing bracket.
	ErrUnfinishedLongString = errors.New("unfinished long string")
	// ErrUnfinishedLongComment is a long comment without its closing bracket.
	ErrUnfinishedLongComment = errors.New("unfinished long comment")
	// ErrLongBracket is "[" followed by "=" signs and no second "[".
	ErrLongBracket = errors.New("invalid long string delimiter")
)

// Error is a lexical error: what went wrong and where.
type Error struct {
	// Err is one of the sentinel errors of this package.
	Err error
	// Offset is the byte offset of the token that failed.
	Offset int
	// Line is the line of the token that failed.
	Line int
}

// Error describes the error the way Lua does, with the token's line.
func (e *Error) Error() string {
	return fmt.Sprintf("line %d: %v", e.Line, e.Err)
}

// Unwrap returns the sentinel error.
func (e *Error) Unwrap() error { return e.Err }

// Lexer reads the tokens of Lua source one by one.
type Lexer struct {
	src []byte
	// start is where the tokens start: after the header of a file.
	start int
	// read is set once the tokens are read.
	read   bool
	tokens []Token
	// next is the index of the token Next returns next.
	next int
	// end is the token Next returns after the last one: EOF, or the
	// position of the token that failed with err.
	end Token
	err error
}

// New returns a lexer for the chunk src, as load() reads it: from its first
// byte, on line 1.
func New(src []byte) *Lexer {
	return newLexer(src, 0)
}

// NewFile returns a lexer for the contents of a Lua file: it skips the header
// that the interpreter skips when it loads a file (see Header) and numbers
// the lines from the first line of the file.
func NewFile(src []byte) *Lexer {
	return newLexer(src, Header(src))
}

// newLexer returns a lexer for src whose tokens start at start.
func newLexer(src []byte, start int) *Lexer {
	var end Token

	return &Lexer{src: src, start: start, read: false, tokens: nil, next: 0, end: end, err: nil}
}

// Next returns the next token; at the end of the source it returns a token of
// kind EOF, as often as it is called. A token that cannot be read is an
// *Error, and Next returns the same error from then on.
func (l *Lexer) Next() (Token, error) {
	l.load()

	if l.next < len(l.tokens) {
		l.next++

		return l.tokens[l.next-1], nil
	}

	return l.end, l.err
}

// Tokens returns every token up to the end of the source, comments included
// and the final EOF left out.
func (l *Lexer) Tokens() ([]Token, error) {
	l.load()

	if l.err != nil {
		return nil, l.err
	}

	tokens := l.tokens[l.next:]
	l.next = len(l.tokens)

	return tokens, nil
}

// load reads the tokens, once.
func (l *Lexer) load() {
	if l.read {
		return
	}

	l.read = true
	r := &reader{src: l.src, tokens: nil, pos: l.start, line: 1}
	l.err = r.read(l.start)
	l.tokens = r.tokens
	l.end = Token{Kind: EOF, Start: r.pos, End: r.pos, Line: r.line, EndLine: r.line}
}
