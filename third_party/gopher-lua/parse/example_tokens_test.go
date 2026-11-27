package parse_test

import (
	"fmt"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
)

// The tokens of a chunk with its trivia: the comments on a statement's
// line and on lines of their own, the blank lines between statements.
func ExampleParseChunk_tokens() {
	src := []byte("local x = 1 -- one\n\n-- about y\nlocal y = x --[[ x ]] + 1\n")
	chunk, err := parse.ParseChunk(src, "example", parse.Options{Tokens: true})
	if err != nil {
		panic(err)
	}
	toks := chunk.Tokens
	for _, stmt := range chunk.Stmts {
		first, last := chunk.TokenRange(stmt)
		fmt.Printf("%q: tokens %d-%d\n", src[toks[first].Span().Start:toks[last].Span().End], first, last)
		breaks := 0 // the line breaks since the statement's last token
		for _, t := range toks[last+1:] {
			if !t.Kind.IsTrivia() {
				break
			}
			switch t.Kind {
			case ast.TokenNewline:
				breaks++
			case ast.TokenComment:
				if breaks == 0 {
					fmt.Printf("  %q on its line\n", t.Text(src))
				} else {
					fmt.Printf("  %q %d lines below, after %d blank lines\n", t.Text(src), breaks, breaks-1)
				}
			}
		}
	}
	// The source back from the tokens, without its comments.
	var out []byte
	for _, t := range toks {
		if t.Kind != ast.TokenComment {
			out = append(out, t.Text(src)...)
		}
	}
	fmt.Printf("%q\n", out)
	// Output:
	// "local x = 1": tokens 0-6
	//   "-- one" on its line
	//   "-- about y" 2 lines below, after 1 blank lines
	// "local y = x --[[ x ]] + 1": tokens 13-25
	// "local x = 1 \n\n\nlocal y = x  + 1\n"
}

// Tokenize reads the tokens of a source that does not parse, here to find
// the modules it requires, up to a lexical error.
func ExampleTokenize() {
	src := []byte("local a = require('a')\nlocal b = require \"b\" +\nlocal c = require [[c]]\nx = 'oops\n")
	toks, err := parse.Tokenize(src, "example", parse.Options{Dialect: parse.LuaJIT})
	var sig []ast.Lexeme // the tokens of the grammar
	for _, t := range toks {
		if !t.Kind.IsTrivia() {
			sig = append(sig, t)
		}
	}
	for i := 0; i+1 < len(sig); i++ {
		if sig[i].Kind != ast.TokenName || string(sig[i].Text(src)) != "require" {
			continue
		}
		arg := sig[i+1]
		if arg.Kind == ast.TokenLParen && i+2 < len(sig) {
			arg = sig[i+2]
		}
		if arg.Kind == ast.TokenString {
			fmt.Printf("require %s at %v\n", arg.Text(src), arg.Span())
		}
	}
	last := toks[len(toks)-1].Span()
	fmt.Printf("read up to %d of %d bytes: %v\n", last.End, len(src), err)
	// Output:
	// require 'a' at 18-21
	// require "b" at 41-44
	// require [[c]] at 65-70
	// read up to 75 of 81 bytes: example:4: unfinished string near ''oops'
}
