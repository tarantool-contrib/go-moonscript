package parse

// go-moonscript patch: a checker of the tokens of a source (ast.Tokens),
// for the token tests and the fuzz test.

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
)

type tokenChecker struct {
	tb     testing.TB
	name   string
	src    []byte
	opts   Options
	toks   ast.Tokens
	errors int
}

func (c *tokenChecker) errorf(format string, args ...any) {
	c.errors++
	if c.errors <= maxSpanErrors {
		c.tb.Helper()
		c.tb.Errorf("%s (%s): %s", c.name, dialectName(c.opts.Dialect), fmt.Sprintf(format, args...))
	}
}

func (c *tokenChecker) text(t ast.Lexeme) string { return string(t.Text(c.src)) }

// checkCover checks that the tokens cover src[:end] (all of it if
// complete, ending with the EOF token), each starting where the one
// before ends, only the EOF token empty, and that the trivia are what
// their kinds say; it returns the end.
func (c *tokenChecker) checkCover(complete bool) int {
	at := 0
	var buf bytes.Buffer
	for i, t := range c.toks {
		s := t.Span()
		buf.Write(t.Text(c.src))
		if s.Start != at {
			c.errorf("token %d %v %v: not at %d", i, t.Kind, s, at)
			return at
		}
		at = s.End
		text := c.text(t)
		switch t.Kind {
		case ast.TokenInvalid:
			c.errorf("token %d %v: invalid", i, s)
		case ast.TokenEOF:
			if i != len(c.toks)-1 || s.Start != len(c.src) || s.End != s.Start {
				c.errorf("EOF token %d of %d at %v", i, len(c.toks), s)
			}
			continue
		case ast.TokenHeader:
			if i != 0 || bytes.ContainsAny([]byte(text), "\r\n") {
				c.errorf("header %d %q", i, text)
			}
		case ast.TokenWhitespace:
			for _, b := range []byte(text) {
				if !isWhite(b) || b == '\n' || b == '\r' {
					c.errorf("white space %d %q", i, text)
				}
			}
			if i > 0 && c.toks[i-1].Kind == ast.TokenWhitespace {
				c.errorf("white space %d after white space", i)
			}
		case ast.TokenNewline:
			if text != "\n" && text != "\r" && text != "\r\n" && text != "\n\r" {
				c.errorf("line break %d %q", i, text)
			}
			if len(text) == 1 && s.End < len(c.src) && (c.src[s.End] == '\n' || c.src[s.End] == '\r') && c.src[s.End] != text[0] {
				c.errorf("line break %d %q before %q", i, text, c.src[s.End])
			}
		case ast.TokenComment:
			if len(text) < 2 || text[:2] != "--" {
				c.errorf("comment %d %q", i, text)
			} else if ast.LongBracketLevel([]byte(text[2:])) < 0 && bytes.ContainsAny([]byte(text), "\r\n") {
				c.errorf("short comment %d %q holds a line break", i, text)
			}
		case ast.TokenString:
			if text[0] != '"' && text[0] != '\'' && ast.LongBracketLevel([]byte(text)) < 0 {
				c.errorf("string %d %q", i, text)
			}
		}
		if s.Start == s.End {
			c.errorf("token %d %v empty at %d", i, t.Kind, s.Start)
		}
	}
	if complete {
		if len(c.toks) == 0 || c.toks[len(c.toks)-1].Kind != ast.TokenEOF {
			c.errorf("no EOF token")
		}
		if !bytes.Equal(buf.Bytes(), c.src) {
			c.errorf("the tokens' texts are not the source")
		}
	} else if !bytes.Equal(buf.Bytes(), c.src[:at]) {
		c.errorf("the tokens' texts are not the source's start")
	}
	return at
}

// checkLexer checks that the tokens other than trivia are those the lexer
// reads from the source, as the parser reads them.
func (c *tokenChecker) checkLexer() {
	ls := newLexer(c.src, c.name, c.opts.Dialect, c.opts.Shebang)
	i := 0
	for {
		ls.nextToken()
		for i < len(c.toks) && c.toks[i].Kind.IsTrivia() {
			i++
		}
		if i == len(c.toks) {
			c.errorf("the lexer reads %q at %d past the tokens", c.src[ls.t.start:ls.t.end], ls.t.start)
			return
		}
		t := c.toks[i]
		if want := tokenKinds[ls.t.typ]; t.Kind != want || t.Span() != (ast.Span{Start: ls.t.start, End: ls.t.end}) {
			c.errorf("token %d: %v %v, the lexer's %v %d-%d", i, t.Kind, t.Span(), want, ls.t.start, ls.t.end)
			return
		}
		i++
		if ls.t.typ == tkEOS {
			break
		}
	}
	if i != len(c.toks) {
		c.errorf("%d tokens after EOF", len(c.toks)-i)
	}
}

// checkChunk checks the tokens of a chunk parsed with Options.Tokens: they
// cover the source, the tokens of the grammar are the lexer's, the
// comments and the header are Chunk.Comments and Chunk.Header, Tokenize
// reads the same tokens, and the token range of every element and of
// every span next to them matches its span.
func (c *tokenChecker) checkChunk(chunk *ast.Chunk) {
	c.checkCover(true)
	c.checkLexer()
	var comments []ast.Span
	for _, t := range c.toks {
		switch t.Kind {
		case ast.TokenComment:
			comments = append(comments, t.Span())
		case ast.TokenHeader:
			if t.Span() != chunk.Header {
				c.errorf("header token %v, Chunk.Header %v", t.Span(), chunk.Header)
			}
		}
	}
	if chunk.Header != (ast.Span{}) && (len(c.toks) == 0 || c.toks[0].Kind != ast.TokenHeader) {
		c.errorf("no header token for %v", chunk.Header)
	}
	if c.opts.Comments {
		if len(comments) != len(chunk.Comments) {
			c.errorf("%d comment tokens, %d comments", len(comments), len(chunk.Comments))
		} else {
			for i, cm := range chunk.Comments {
				if cm.Span != comments[i] {
					c.errorf("comment %d at %v, its token at %v", i, cm.Span, comments[i])
				}
				if cm.Long != (ast.LongBracketLevel(c.src[cm.Span.Start+2:cm.Span.End]) >= 0) {
					c.errorf("comment %d at %v: long %v", i, cm.Span, cm.Long)
				}
			}
		}
	}
	toks, err := Tokenize(c.src, c.name, c.opts)
	if err != nil || !reflect.DeepEqual(toks, c.toks) {
		c.errorf("Tokenize: %v, %d tokens, the chunk's %d", err, len(toks), len(c.toks))
	}
	c.checkRange(chunk.StmtsSpan, "chunk block", true)
	ast.Inspect(chunk, func(n ast.Element) bool {
		_, isChunk := n.(*ast.Chunk)
		c.checkRange(n.Span(), fmt.Sprintf("%T", n), !isChunk)
		if e, ok := n.(ast.Expr); ok && e.Parens().Count > 0 {
			first, last := c.toks.Range(e.OuterSpan())
			c.checkRange(e.OuterSpan(), "parentheses", true)
			if first <= last && (c.toks[first].Kind != ast.TokenLParen || c.toks[last].Kind != ast.TokenRParen) {
				c.errorf("parentheses %v from %v to %v", e.OuterSpan(), c.toks[first].Kind, c.toks[last].Kind)
			}
		}
		c.checkSpanFields(n)
		return true
	})
}

var (
	spanType  = reflect.TypeOf(ast.Span{})
	spansType = reflect.TypeOf([]ast.Span(nil))
)

// checkSpanFields checks the ranges of the spans held next to an
// element's names and blocks (NameSpans, StmtsSpan, ...).
func (c *tokenChecker) checkSpanFields(n ast.Element) {
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return
	}
	v = v.Elem()
	for i := 0; i < v.NumField(); i++ {
		f, fv := v.Type().Field(i), v.Field(i)
		if !f.IsExported() || f.Name == "Header" { // trivia
			continue
		}
		switch f.Type {
		case spanType:
			s := fv.Interface().(ast.Span)
			if s != (ast.Span{}) || f.Name == "StmtsSpan" {
				c.checkRange(s, fmt.Sprintf("%T.%s", n, f.Name), true)
			}
		case spansType:
			for _, s := range fv.Interface().([]ast.Span) {
				c.checkRange(s, fmt.Sprintf("%T.%s", n, f.Name), true)
			}
		}
	}
}

// checkRange checks that the token range of s is the tokens from the one
// starting at its start to the one ending at its end, not trivia if
// significant; that of an empty span is empty, before the token there.
func (c *tokenChecker) checkRange(s ast.Span, what string, significant bool) {
	first, last := c.toks.Range(s)
	if s.Start == s.End {
		if last != first-1 || first >= len(c.toks) || c.toks[first].Span().Start != s.Start {
			c.errorf("%s: empty span %v: range %d-%d", what, s, first, last)
		}
		return
	}
	if first > last || last >= len(c.toks) || c.toks[first].Span().Start != s.Start || c.toks[last].Span().End != s.End {
		c.errorf("%s: span %v: range %d-%d", what, s, first, last)
		return
	}
	if significant && (c.toks[first].Kind.IsTrivia() || c.toks[last].Kind.IsTrivia()) {
		c.errorf("%s: span %v %q: from %v to %v", what, s, c.src[s.Start:s.End], c.toks[first].Kind, c.toks[last].Kind)
	}
	if c.toks.At(s.Start) != first || c.toks.At(s.End-1) != last {
		c.errorf("%s: span %v: At %d %d, range %d-%d", what, s, c.toks.At(s.Start), c.toks.At(s.End-1), first, last)
	}
}

// checkTokens checks the tokens of a chunk that src parses to with opts
// (and Options.Tokens), and that the chunk is the one parsed without
// them: the same tree, lines and spans, comments and header.
func checkTokens(tb testing.TB, name string, src []byte, opts Options, chunk *ast.Chunk) int {
	tb.Helper()
	c := &tokenChecker{tb: tb, name: name, src: src, opts: opts, toks: chunk.Tokens}
	c.checkChunk(chunk)
	opts.Tokens = false
	plain, err := ParseChunk(src, name, opts)
	if err != nil {
		c.errorf("parses only with its tokens: %v", err)
	} else if !reflect.DeepEqual(plain.Stmts, chunk.Stmts) || plain.StmtsSpan != chunk.StmtsSpan ||
		plain.Header != chunk.Header || !reflect.DeepEqual(plain.Comments, chunk.Comments) || plain.Tokens != nil {
		c.errorf("the trees with and without tokens differ")
	}
	return c.errors
}

// checkTokenize checks the tokens Tokenize reads from src, which may not
// parse: they cover the source, or its start up to an error.
func checkTokenize(tb testing.TB, name string, src []byte, opts Options) (ast.Tokens, error) {
	tb.Helper()
	toks, err := Tokenize(src, name, opts)
	c := &tokenChecker{tb: tb, name: name, src: src, opts: opts, toks: toks}
	if err != nil {
		if _, ok := err.(*Error); !ok {
			c.errorf("error %T %v", err, err)
		}
		c.checkCover(false)
		return toks, err
	}
	c.checkCover(true)
	c.checkLexer()
	return toks, nil
}
