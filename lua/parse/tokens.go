package parse

// go-moonscript patch: the tokens of a source as written (see
// ast.Tokens).

import (
	"math"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
)

// Tokenize reads the tokens of src as the lexer of opts.Dialect reads
// them, with the trivia, without parsing it: what ParseChunk keeps in
// Chunk.Tokens with Options.Tokens, also for a source that does not parse.
// It skips the header as ParseChunk does (Options.Shebang); Options.Comments
// and Options.Tokens are not read. The tokens end with TokenEOF if the
// whole source was read.
//
// A lexical error (an unfinished string or long comment, a malformed
// number, an invalid escape sequence or long bracket, ...) is an *Error,
// with the message and line lua5.1 or LuaJIT give it; the tokens are then
// those read before the token in error, which starts where the last of
// them ends. Tokenize reads each token the parser would read: a source
// that ParseChunk lexes without an error, Tokenize does too.
func Tokenize(src []byte, name string, opts Options) (tokens ast.Tokens, err error) {
	if uint64(len(src)) > math.MaxUint32 {
		return nil, errTooLarge(name)
	}
	ls := allocLexer(src, name, opts.Dialect, true)
	defer func() {
		if e := recover(); e != nil {
			pe, ok := e.(*Error)
			if !ok {
				panic(e)
			}
			tokens, err = ls.tokens, pe
		}
	}()
	ls.skipHeader(opts.Shebang)
	for {
		ls.nextToken()
		if ls.t.typ == tkEOS {
			return ls.tokens, nil
		}
	}
}

// errTooLarge is the error for the tokens of a source of 4 GiB or more,
// whose offsets ast.Lexeme cannot hold.
func errTooLarge(name string) *Error {
	return &Error{Pos: ast.Position{Source: name, Line: NoLine}, Message: name + ": source too large for its tokens"}
}
