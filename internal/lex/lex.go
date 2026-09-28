// Package lex splits Lua source into tokens. It knows the lexical grammar of
// Lua 5.1 to 5.5, LuaJIT and Tarantool, and reports every token's exact byte
// span and lines, so a tool can rebuild source from the original bytes: keep
// the tokens it wants byte for byte and decide what goes between them.
//
// Where the dialects differ the lexer reads the union of them, so it never
// fails on source that one of them accepts:
//
//   - A long bracket, [[...]] or [==[...]==], string or comment, ends at the
//     first closing bracket of its level. Lua 5.1 refuses a "[[" inside a
//     level-0 long bracket (LUA_COMPAT_LSTR), so valid source never has one;
//     no dialect counts nesting.
//   - A short string takes every escape: "\" before a line break, and "\z"
//     (Lua 5.2+, LuaJIT), which skips the white space after it, line breaks
//     included. \x, \u{...} and decimal escapes need nothing special to find
//     the end of the string. Escapes are not decoded or validated.
//   - A numeral is scanned the way LuaJIT scans it: a digit, or "." and a
//     digit, then every identifier character, every ".", and a sign after an
//     exponent letter (e or E, p or P after 0x). That takes in hexadecimal
//     floats (0x1p-4) and LuaJIT's 1LL, 0x10ULL and 2i. Numerals are not
//     validated.
//   - Bytes 0x80 to 0xFF are identifier characters, as in LuaJIT (and in PUC
//     Lua built with LUA_UCID).
//   - The operators of every dialect are tokens: "//", "&", "|", "~", "<<" and
//     ">>" (Lua 5.3+) and "::" (Lua 5.2+, LuaJIT) as well as those of Lua 5.1.
//   - A line break is "\n", "\r", "\r\n" or "\n\r", each one line.
//
// The union is also the most cautious answer to where tokens meet: two
// tokens the lexer reads as separate are separate in every dialect. It is not
// always the finest one. Lua 5.1 ends a numeral with an exponent or a hex
// prefix before ".." (1e5..x is 1e5 .. x there), and Lua 5.2 and 5.3 end a
// numeral before a letter that cannot continue it (3then is 3 then, and
// 0x10then is 0x10 then); every other dialect rejects both, and the lexer
// reads each as one numeral.
//
// Like Lua's own lexer this one leaves grammar to a parser: a byte that
// starts no token is a token of kind Invalid, and which names are reserved
// words is not its business. goto is a name in some LuaJIT builds, and Lua
// 5.5.1 reads global as a keyword only where it starts a declaration. What
// the lexer does refuse is what no dialect can read: a string, long string
// or long comment without its end, and "[" followed by "=" signs but no
// second "[".
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
	// Symbol is an operator or punctuation: + - * / // % ^ # & ~ | << >>
	// == ~= <= >= < > = ( ) { } [ ] :: ; : , . .. ...
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
	src  []byte
	pos  int
	line int
	err  error
}

// New returns a lexer for the chunk src, as load() reads it: from its first
// byte, on line 1.
func New(src []byte) *Lexer {
	return &Lexer{src: src, pos: 0, line: 1, err: nil}
}

// NewFile returns a lexer for the contents of a Lua file: it skips the header
// that the interpreter skips when it loads a file (see Header) and numbers
// the lines from the first line of the file.
func NewFile(src []byte) *Lexer {
	return &Lexer{src: src, pos: Header(src), line: 1, err: nil}
}

// Next returns the next token; at the end of the source it returns a token of
// kind EOF, as often as it is called. A token that cannot be read is an
// *Error, and Next returns the same error from then on.
func (l *Lexer) Next() (Token, error) {
	if l.err != nil {
		return Token{Kind: EOF, Start: l.pos, End: l.pos, Line: l.line, EndLine: l.line}, l.err
	}

	l.skipSpace()

	start, line := l.pos, l.line

	if l.pos >= len(l.src) {
		return Token{Kind: EOF, Start: start, End: start, Line: line, EndLine: line}, nil
	}

	kind, err := l.scan()
	if err != nil {
		l.err = &Error{Err: err, Offset: start, Line: line}

		return Token{Kind: EOF, Start: start, End: start, Line: line, EndLine: line}, l.err
	}

	return Token{Kind: kind, Start: start, End: l.pos, Line: line, EndLine: l.line}, nil
}

// Tokens returns every token up to the end of the source, comments included
// and the final EOF left out.
func (l *Lexer) Tokens() ([]Token, error) {
	var tokens []Token

	for {
		tok, err := l.Next()
		if err != nil {
			return nil, err
		}

		if tok.Kind == EOF {
			return tokens, nil
		}

		tokens = append(tokens, tok)
	}
}

// skipSpace moves past white space, counting line breaks.
func (l *Lexer) skipSpace() {
	for l.pos < len(l.src) {
		if isLineBreakByte(l.src[l.pos]) {
			l.newline()

			continue
		}

		if !IsSpace(l.src[l.pos]) {
			return
		}

		l.pos++
	}
}

// newline moves past the line break at the current position.
func (l *Lexer) newline() {
	l.pos += LineBreak(l.src, l.pos)
	l.line++
}
