package ast

// go-moonscript patch: the tokens of a source, as written (see doc.go).

import (
	"math"
	"sort"
)

// TokenKind is the kind of a Lexeme: trivia (the header, white space, a
// line break, a comment), a name, a literal, a keyword, an operator or
// punctuation, a character that is no Lua token, or the end of the source.
type TokenKind uint8

const (
	// TokenInvalid is no token: the zero TokenKind.
	TokenInvalid TokenKind = iota
	// TokenEOF is the end of the source: the last token, empty, at
	// len(src).
	TokenEOF

	// Trivia: the text between the tokens of the grammar.

	// TokenHeader is what the lexer skips before the first token as no
	// Lua text (Chunk.Header): a UTF-8 byte order mark and a first line
	// starting with "#", without its line break.
	TokenHeader
	// TokenWhitespace is a run of white space within a line: spaces, tabs,
	// "\v" and "\f".
	TokenWhitespace
	// TokenNewline is one line break, as the lexer counts them: "\n",
	// "\r", "\r\n" or "\n\r".
	TokenNewline
	// TokenComment is a comment: a short one from "--" to the end of the
	// line, without the line break, or a long one, "--[[ ... ]]" or
	// "--[==[ ... ]==]", whose text after the "--" starts with a long
	// bracket (LongBracketLevel).
	TokenComment

	// TokenName is a name (and "goto" under Lua 5.1).
	TokenName
	// TokenNumber is a numeral.
	TokenNumber
	// TokenString is a string literal; its text starts with its quote, '
	// or ", or its long bracket, "[[" or "[==[".
	TokenString
	// TokenOther is a character that makes a token of its own but none of
	// the grammar ("@", "$", "!", "~" alone, a control character, a byte
	// 128-255 under Lua 5.1, ...): a source holding one lexes, but does
	// not parse.
	TokenOther

	// Keywords.

	TokenAnd
	TokenBreak
	TokenDo
	TokenElse
	TokenElseif
	TokenEnd
	TokenFalse
	TokenFor
	TokenFunction
	TokenGoto // under LuaJIT; a name under Lua 5.1
	TokenIf
	TokenIn
	TokenLocal
	TokenNil
	TokenNot
	TokenOr
	TokenRepeat
	TokenReturn
	TokenThen
	TokenTrue
	TokenUntil
	TokenWhile

	// Operators and punctuation.

	TokenPlus        // +
	TokenMinus       // -
	TokenStar        // *
	TokenSlash       // /
	TokenPercent     // %
	TokenCaret       // ^
	TokenHash        // #
	TokenEq          // ==
	TokenNe          // ~=
	TokenLe          // <=
	TokenGe          // >=
	TokenLt          // <
	TokenGt          // >
	TokenAssign      // =
	TokenLParen      // (
	TokenRParen      // )
	TokenLBrace      // {
	TokenRBrace      // }
	TokenLBracket    // [
	TokenRBracket    // ]
	TokenDoubleColon // :: (LuaJIT; two ":" under Lua 5.1)
	TokenSemicolon   // ;
	TokenColon       // :
	TokenComma       // ,
	TokenDot         // .
	TokenConcat      // ..
	TokenDots        // ...
)

const (
	firstKeyword    = TokenAnd
	lastKeyword     = TokenWhile
	firstOperator   = TokenPlus
	lastOperator    = TokenDots
	firstTriviaKind = TokenHeader
	lastTriviaKind  = TokenComment
)

var tokenKindNames = [...]string{
	TokenInvalid:    "<invalid>",
	TokenEOF:        "<eof>",
	TokenHeader:     "<header>",
	TokenWhitespace: "<whitespace>",
	TokenNewline:    "<newline>",
	TokenComment:    "<comment>",
	TokenName:       "<name>",
	TokenNumber:     "<number>",
	TokenString:     "<string>",
	TokenOther:      "<other>",

	TokenAnd: "and", TokenBreak: "break", TokenDo: "do", TokenElse: "else",
	TokenElseif: "elseif", TokenEnd: "end", TokenFalse: "false", TokenFor: "for",
	TokenFunction: "function", TokenGoto: "goto", TokenIf: "if", TokenIn: "in",
	TokenLocal: "local", TokenNil: "nil", TokenNot: "not", TokenOr: "or",
	TokenRepeat: "repeat", TokenReturn: "return", TokenThen: "then",
	TokenTrue: "true", TokenUntil: "until", TokenWhile: "while",

	TokenPlus: "+", TokenMinus: "-", TokenStar: "*", TokenSlash: "/",
	TokenPercent: "%", TokenCaret: "^", TokenHash: "#", TokenEq: "==",
	TokenNe: "~=", TokenLe: "<=", TokenGe: ">=", TokenLt: "<", TokenGt: ">",
	TokenAssign: "=", TokenLParen: "(", TokenRParen: ")", TokenLBrace: "{",
	TokenRBrace: "}", TokenLBracket: "[", TokenRBracket: "]",
	TokenDoubleColon: "::", TokenSemicolon: ";", TokenColon: ":",
	TokenComma: ",", TokenDot: ".", TokenConcat: "..", TokenDots: "...",
}

// String returns the text of a keyword or an operator ("and", "~="), or
// the kind's name in angle brackets ("<name>", "<comment>", "<eof>").
func (k TokenKind) String() string {
	if int(k) < len(tokenKindNames) {
		return tokenKindNames[k]
	}
	return tokenKindNames[TokenInvalid]
}

// IsTrivia reports whether the kind is trivia: the header, white space, a
// line break or a comment, text that the grammar skips.
func (k TokenKind) IsTrivia() bool { return firstTriviaKind <= k && k <= lastTriviaKind }

// IsKeyword reports whether the kind is a keyword ("goto" is one under
// LuaJIT).
func (k TokenKind) IsKeyword() bool { return firstKeyword <= k && k <= lastKeyword }

// IsOperator reports whether the kind is an operator or punctuation.
func (k TokenKind) IsOperator() bool { return firstOperator <= k && k <= lastOperator }

// Lexeme is a token of a source as written: its kind and its span, the
// bytes src[Span().Start:Span().End]. The tokens of a source (Tokens) are
// every token of the grammar and every piece of trivia, so that their
// texts in order are the source. (Token is gopher-lua's former token type,
// which nothing uses.) A Lexeme is 12 bytes: its offsets are 32-bit, as
// SpanBase's are.
type Lexeme struct {
	Kind       TokenKind
	start, end uint32
}

// NewLexeme returns the token of the kind with the span; a span that is
// none or that ends past 4 GiB is kept as the zero Span (SpanBase.SetSpan).
func NewLexeme(kind TokenKind, s Span) Lexeme {
	if s.Start < 0 || s.End < s.Start || uint64(s.End) > math.MaxUint32 {
		return Lexeme{Kind: kind}
	}
	return Lexeme{Kind: kind, start: uint32(s.Start), end: uint32(s.End)}
}

// Span returns where the token lies in the source.
func (t Lexeme) Span() Span { return Span{Start: int(t.start), End: int(t.end)} }

// Text returns the token's text in src, the source it was read from.
func (t Lexeme) Text(src []byte) []byte { return src[t.start:t.end] }

// Tokens are the tokens of a source in order, as parse.ParseChunk (with
// parse.Options.Tokens) and parse.Tokenize return them: each token of the
// grammar, each piece of trivia (TokenKind.IsTrivia) and, if the whole
// source was read, the empty TokenEOF last. They cover the source: each
// token starts where the one before ends, the first at 0, so that the
// concatenation of their texts is the source, byte for byte. Only the EOF
// token is empty.
//
// Trivia are tokens of their own, not attached to the tokens of the
// grammar: white space within a line is one token, each line break is
// one, each comment one. A blank line between two statements is two line
// breaks between the last token of the one and the first token of the
// other; a comment on the line of a token has no line break between them.
type Tokens []Lexeme

// Range returns the indices of the first and the last token in the span:
// from the first token starting at or after s.Start to the last one ending
// at or before s.End, without the EOF token. For the span of an element,
// which starts at the start of its first token and ends at the end of its
// last one, these are its first and last tokens, with the trivia between
// them. For an empty span, last is first-1 and first is the token that
// starts at s.Start, or after it.
func (ts Tokens) Range(s Span) (first, last int) {
	first = sort.Search(len(ts), func(i int) bool { return int(ts[i].start) >= s.Start })
	last = sort.Search(len(ts), func(i int) bool { return int(ts[i].end) > s.End }) - 1
	if last >= 0 && ts[last].start == ts[last].end { // EOF
		last--
	}
	return first, last
}

// At returns the index of the token holding the byte at offset: the token
// with Start <= offset < End, the EOF token at len(src), or -1 if no token
// holds it.
func (ts Tokens) At(offset int) int {
	i := sort.Search(len(ts), func(i int) bool { return int(ts[i].start) > offset }) - 1
	if i < 0 {
		return -1
	}
	if t := ts[i]; offset >= int(t.end) && !(t.start == t.end && offset == int(t.start)) {
		return -1
	}
	return i
}

// LongBracketLevel returns the level of the long bracket that b starts
// with: 0 for "[[", 2 for "[==[", or -1 if b does not start with one. A
// comment is long if LongBracketLevel(text[2:]) >= 0, a string literal if
// its text starts with "[".
func LongBracketLevel(b []byte) int {
	if len(b) == 0 || b[0] != '[' {
		return -1
	}
	n := 1
	for n < len(b) && b[n] == '=' {
		n++
	}
	if n < len(b) && b[n] == '[' {
		return n - 1
	}
	return -1
}
