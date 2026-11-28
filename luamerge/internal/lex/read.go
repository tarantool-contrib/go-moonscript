package lex

import (
	"bytes"
	"errors"
	"slices"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
)

// longCommentStart is the offset in a comment of what makes it long: the
// long bracket after its "--".
const longCommentStart = len("--")

// reader reads the tokens of a source with the fork's lexer, and the
// tokens it stops at (see read).
type reader struct {
	src    []byte
	tokens []Token
	// pos is the offset up to which the source is read, line the line it
	// is on.
	pos  int
	line int
}

// read reads the tokens of r.src from start on, which is on line 1: those of
// LuaJIT's lexer (parse.Tokenize), from start, and after a token it stops at
// that another dialect reads (see repair), from after that token. A lexical
// error that no dialect makes is an *Error.
func (r *reader) read(start int) error {
	r.pos = start
	options := parse.Options{Dialect: parse.LuaJIT, Comments: false, Shebang: false, Tokens: false, Lua52Compat: false}

	for {
		r.guard()

		tokens, err := parse.Tokenize(r.src[r.pos:], "", options)
		r.add(tokens)

		if err == nil {
			return nil
		}

		err = r.repair(err)
		if err != nil {
			return err
		}
	}
}

// guard reads the tokens at r.pos that the fork's lexer, which reads a file,
// would take for its header: a "#" at r.pos is a token of its own, and a UTF-8
// byte order mark is the start of a name, as in the middle of a file.
func (r *reader) guard() {
	for r.pos < len(r.src) {
		switch {
		case r.src[r.pos] == '#':
			r.emit(Symbol, r.pos+1, false)
		case bytes.HasPrefix(r.src[r.pos:], []byte(BOM)):
			end := r.pos
			for end < len(r.src) && isIdent(r.src[end]) {
				end++
			}

			r.emit(Name, end, false)
		default:
			return
		}
	}
}

// emit adds the token of kind from r.pos to end.
func (r *reader) emit(kind Kind, end int, notLuaJIT bool) {
	line := r.line
	r.line += lineBreaks(r.src[r.pos:end])
	r.tokens = append(r.tokens, Token{
		Kind: kind, Start: r.pos, End: end, Line: line, EndLine: r.line, NotLuaJIT: notLuaJIT,
	})
	r.pos = end
}

// add adds the tokens the fork's lexer read from r.pos on, in the kinds of
// this package: without the white space and the line breaks, "&", "|" and
// "~" as symbols, any other character of no token of the grammar as Invalid,
// and "//", "<<" and ">>", two tokens to it, as one (see addShift).
func (r *reader) add(tokens ast.Tokens) {
	base := r.pos
	// About every other token of the fork's is trivia.
	r.tokens = slices.Grow(r.tokens, len(tokens)/2+1)

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		span := tok.Span()
		r.pos = base + span.Start
		end := base + span.End

		switch kind := tok.Kind; {
		case kind == ast.TokenEOF:
			return
		case kind == ast.TokenNewline:
			r.line++
			r.pos = end
		case kind == ast.TokenWhitespace, kind == ast.TokenHeader:
			// The fork reads no header after guard.
			r.pos = end
		case kind == ast.TokenComment:
			r.emit(commentKind(r.src[r.pos:end]), end, false)
		case kind == ast.TokenString && r.src[r.pos] == '[':
			r.emit(LongString, end, false)
		case kind == ast.TokenString:
			r.emit(String, end, false)
		case kind == ast.TokenNumber:
			r.emit(Number, end, false)
		case kind == ast.TokenName, kind.IsKeyword():
			r.emit(Name, end, false)
		case kind == ast.TokenOther:
			r.emit(otherKind(r.src[r.pos:end]), end, false)
		case i+1 < len(tokens) && joinsNext(kind, tokens[i+1].Kind):
			i = r.addShift(tokens, i+1, base)
		default:
			r.emit(Symbol, end, false)
		}
	}
}

// addShift adds "//", "<<" or ">>" at r.pos, whose second character is the
// token at i, and returns the index of the last token it read. The fork
// reads "<<=" as "<" and "<=", which is "<<" and "=", and that "=" is the
// start of "==" when "=" follows.
func (r *reader) addShift(tokens ast.Tokens, i, base int) int {
	r.emit(Symbol, r.pos+len("//"), false)

	if tokens[i].Kind != ast.TokenLe && tokens[i].Kind != ast.TokenGe {
		return i
	}

	for i+1 < len(tokens) && (tokens[i+1].Kind == ast.TokenAssign || tokens[i+1].Kind == ast.TokenEq) {
		i++

		r.emit(Symbol, r.pos+len("=="), false)

		if tokens[i].Kind == ast.TokenAssign {
			return i
		}
	}

	if end := base + tokens[i].Span().End; r.pos < end {
		r.emit(Symbol, end, false)
	}

	return i
}

// joinsNext reports whether a token of kind and one of next right after it
// start a symbol of Lua 5.3 that the fork reads as two tokens: "//", "<<" or
// ">>", and "<<=" and ">>=" as "<" and "<=", ">" and ">=".
func joinsNext(kind, next ast.TokenKind) bool {
	return kind == ast.TokenSlash && next == ast.TokenSlash ||
		kind == ast.TokenLt && (next == ast.TokenLt || next == ast.TokenLe) ||
		kind == ast.TokenGt && (next == ast.TokenGt || next == ast.TokenGe)
}

// otherKind returns the kind of text, a character the fork reads as no token
// of the grammar: Symbol for the operators of Lua 5.3 "&", "|" and "~",
// Invalid for any other.
func otherKind(text []byte) Kind {
	switch string(text) {
	case "&", "|", "~":
		return Symbol
	default:
		return Invalid
	}
}

// commentKind returns the kind of the comment text: LongComment when a long
// bracket follows its "--".
func commentKind(text []byte) Kind {
	if len(text) > longCommentStart && text[longCommentStart] == '[' {
		if _, ok := level(text, longCommentStart); ok {
			return LongComment
		}
	}

	return Comment
}

// level counts the "=" signs after the bracket at text[i] and reports whether
// the same bracket follows them: "[==[" gives 2 and true, "[==x" 2 and false.
func level(text []byte, i int) (int, bool) {
	j := i + 1
	for j < len(text) && text[j] == '=' {
		j++
	}

	return j - i - 1, j < len(text) && text[j] == text[i]
}

// repair reads the token the fork's lexer stopped at with err, at r.pos, when
// another dialect reads it (see the package documentation): a short string
// up to its closing quote, taking the character after a backslash for its
// escape, but for "\z" and the white space after it and a backslash before a
// line break, and a numeral as LuaJIT scans it. It returns the error, an
// *Error, for any other token, which no dialect reads: a short string
// without its end, an unfinished long string or long comment, and "[" and
// "=" signs without a second "[".
func (r *reader) repair(err error) error {
	start := r.pos
	char := r.src[start]

	switch {
	case char == '"' || char == '\'':
		end, ok := shortStringEnd(r.src, start)
		if !ok {
			return &Error{Err: ErrUnfinishedString, Offset: start, Line: r.line}
		}

		r.emit(String, end, true)
	case isDigit(char) || char == '.' && start+1 < len(r.src) && isDigit(r.src[start+1]):
		r.emit(Number, numberEnd(r.src, start), true)
	default:
		sentinel := ErrLongBracket

		if syntaxErr, ok := errors.AsType[*parse.Error](err); ok {
			switch syntaxErr.Message {
			case "unfinished long string":
				sentinel = ErrUnfinishedLongString
			case "unfinished long comment":
				sentinel = ErrUnfinishedLongComment
			}
		}

		return &Error{Err: sentinel, Offset: start, Line: r.line}
	}

	return nil
}

// shortStringEnd returns the end of the short string that starts at src[i],
// and false when it has none (see repair).
func shortStringEnd(src []byte, i int) (int, bool) {
	quote := src[i]

	for i++; i < len(src); {
		switch char := src[i]; {
		case char == quote:
			return i + 1, true
		case isLineBreakByte(char):
			return i, false
		case char != '\\':
			i++
		case i+1 == len(src):
			return i, false
		case isLineBreakByte(src[i+1]):
			i += 1 + LineBreak(src, i+1)
		case src[i+1] == 'z':
			i += len(`\z`)
			for i < len(src) && IsSpace(src[i]) {
				i++
			}
		default:
			i += len(`\\`)
		}
	}

	return i, false
}

// numberEnd returns the end of the numeral that starts at src[i], read the
// way LuaJIT does: every identifier character, every ".", and a sign right
// after an exponent letter, which is e or E, or p or P after 0x.
func numberEnd(src []byte, i int) int {
	hex := src[i] == '0' && i+1 < len(src) && (src[i+1] == 'x' || src[i+1] == 'X')
	prev := byte(0)

	for ; i < len(src); i++ {
		char := src[i]

		sign := (char == '+' || char == '-') && isExponent(prev, hex)
		if !isIdent(char) && char != '.' && !sign {
			break
		}

		prev = char
	}

	return i
}

// isExponent reports whether c is an exponent letter of a decimal (hex false)
// or hexadecimal numeral.
func isExponent(c byte, hex bool) bool {
	if hex {
		return c == 'p' || c == 'P'
	}

	return c == 'e' || c == 'E'
}

// lineBreaks returns the number of line breaks in text, as Lua counts them.
func lineBreaks(text []byte) int {
	n := 0

	for i := 0; i < len(text); i++ {
		if k := LineBreak(text, i); k > 0 {
			n++
			i += k - 1
		}
	}

	return n
}
