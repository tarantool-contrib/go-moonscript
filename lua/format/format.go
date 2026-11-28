package format

import (
	"errors"
	"io"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/parse"
)

// Quotes is the quote style of short string literals.
type Quotes uint8

const (
	// QuotesKeep keeps the quotes of the source; strings without one get
	// double quotes.
	QuotesKeep Quotes = iota
	// QuotesDouble writes double quotes, unless the string holds a double
	// quote and no single one.
	QuotesDouble
	// QuotesSingle writes single quotes, unless the string holds a single
	// quote and no double one.
	QuotesSingle
)

// Options are the options of the formatter and the printer. The zero
// Options are Lua 5.1, lines of 100 columns, 4 spaces of indentation.
type Options struct {
	// Dialect is the language: the parser's (Source), the keywords and
	// names (goto is a keyword of LuaJIT, whose names may hold bytes
	// 128-255).
	Dialect parse.Dialect
	// Width is the width of a line, in columns: a group that does not fit
	// is broken over lines. 0 means 100.
	Width int
	// IndentWidth is the number of spaces of a level of indentation, or
	// the width of a tab with UseTabs. 0 means 4.
	IndentWidth int
	// UseTabs indents with tabs.
	UseTabs bool
	// Quotes is the quote style of short strings.
	Quotes Quotes
	// TrailingComma ends the tables laid out on more than one line with a
	// ",": without it, they keep the trailing separator of the source.
	// A table on one line has none.
	TrailingComma bool
	// KeepSemicolons keeps the ";" after a statement in the source; without
	// it they are dropped. A ";" before a statement that starts with "("
	// is always written.
	KeepSemicolons bool
}

func (o *Options) normalize() {
	if o.Width <= 0 {
		o.Width = 100
	}
	if o.IndentWidth <= 0 {
		o.IndentWidth = 4
	}
}

func (o *Options) indent() string {
	if o.UseTabs {
		return "\t"
	}
	return strings.Repeat(" ", o.IndentWidth)
}

// Source formats a Lua source: it parses it (in opts.Dialect, skipping a
// first "#" line under Lua 5.1 as lua5.1 does running a file) and prints
// it with its comments, keeping the forms of its literals, keys and calls,
// its parentheses and its blank lines (see Chunk). A syntax error is a
// *parse.Error.
func Source(src []byte, opts Options) ([]byte, error) {
	chunk, err := parse.ParseChunk(src, "source", parse.Options{Dialect: opts.Dialect, Shebang: true, Tokens: true})
	if err != nil {
		return nil, err
	}
	p := newPrinter(opts)
	p.cm = NewCommentMap(chunk)
	p.src, p.toks, p.lines = src, chunk.Tokens, chunk.Lines
	return p.printChunk(chunk, len(src)+len(src)/4)
}

// Chunk writes a chunk, formatted: with the comments of cm (nil for none),
// and, if the chunk has its tokens (parse.Options.Tokens), keeping for
// each element that has a span in the source what its author chose:
//
//   - the text of string literals (their quotes changed only with
//     Options.Quotes) and numerals;
//   - a.b or a["b"], {b = 1} or {["b"] = 1}, f"s" and f{...} or f("s");
//   - the parentheses ((a + b), ((f())));
//   - "elseif" or "else if";
//   - an empty line between statements and between fields (one of
//     several), the header line ("#!...");
//   - a block statement or function on one line with at most a statement
//     in each block stays on one line if it fits; a table with a line
//     break after its "{" is laid out on lines.
//
// The rest is laid out anew: the indentation, the spaces, the line breaks
// within the width. The elements without a span (the elements a
// transformation added) are printed as Node prints them. The chunk may
// have been transformed after parsing, if the elements that keep their
// spans keep their meaning: a literal whose value changed is written anew,
// but a span moved to another source is not detected.
func Chunk(w io.Writer, chunk *ast.Chunk, cm CommentMap, opts Options) error {
	if chunk == nil {
		return errors.New("format: nil chunk")
	}
	p := newPrinter(opts)
	p.cm = cm
	if chunk.Tokens != nil && chunk.Lines != nil {
		p.src = chunk.Lines.Source()
		p.toks = chunk.Tokens
		p.lines = chunk.Lines
	}
	size := 4096
	if p.src != nil {
		size = len(p.src) + len(p.src)/4
	}
	out, err := p.printChunk(chunk, size)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

// printChunk prints a chunk, and checks that its comments were printed.
func (p *printer) printChunk(chunk *ast.Chunk, size int) ([]byte, error) {
	out, err := p.print(chunk, size)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, cs := range p.cm {
		total += len(cs)
	}
	if p.emitted != total {
		return nil, errors.New("format: comments of the map are not in the tree")
	}
	return out, nil
}

// Node writes a tree in the canonical style, whatever its source: a
// *ast.Chunk (a block of statements: &ast.Chunk{Stmts: stmts}), an
// ast.Stmt or an ast.Expr. The tree may have been built or transformed by
// other code; it is printed so that parsing the text in opts.Dialect gives
// the same tree, which compiles to the same code (but for the lines):
//
//   - parentheses where precedence, associativity or the grammar need them
//     ((a + b) * c, a - (b - c), ("s"):rep(2), (f or g)()), and around a
//     call or "..." truncated to one value (AdjustRet) at the end of a
//     list;
//   - a.b if the key is a name of the dialect, else a["key"];
//   - strings double-quoted (Options.Quotes), with the escapes of Lua 5.1
//     for the bytes that need one; numerals as their Value if it is one,
//     else the shortest that converts back (1e999 for the infinity, 0/0
//     for NaN, a unary minus for a negative value);
//   - a ";" before a statement starting with "(", spaces where two tokens
//     would make one ("- -x", "a .. .5", "t[ [[s]] ]").
//
// A tree that cannot be written so is an error: a name that is no name
// (of a variable, a method, a function statement), an assignment to
// something else than a name or an index, a node of unknown type, a nil
// where an element is needed.
func Node(w io.Writer, node ast.Element, opts Options) error {
	p := newPrinter(opts)
	out, err := p.print(node, 4096)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

func newPrinter(opts Options) *printer {
	opts.normalize()
	return &printer{opts: opts, jit: opts.Dialect == parse.LuaJIT}
}

func (p *printer) print(node ast.Element, size int) (out []byte, err error) {
	defer func() {
		if e := recover(); e != nil {
			pe, ok := e.(printError)
			if !ok {
				panic(e)
			}
			out, err = nil, pe.err
		}
	}()
	if isNil(node) {
		p.fail("nil node")
	}
	var d doc
	chunk := false
	switch n := node.(type) {
	case *ast.Chunk:
		d = p.chunk(n)
		chunk = true
	case ast.Stmt:
		lead := p.open(n)
		body := p.stmt(n)
		rest, trail := p.close()
		d = cat(lead, body, rest, trail)
	case ast.Expr:
		d = p.expr(n, 0, false)
	default:
		p.fail("cannot print a %T", node)
	}
	r := newRenderer(p.opts.Width, p.opts.indent(), p.opts.IndentWidth, size)
	r.render(d)
	out = r.out
	if chunk && len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out, nil
}

func (p *printer) chunk(c *ast.Chunk) doc {
	var header doc
	if p.src != nil && c.Header != (ast.Span{}) && c.Header.End <= len(p.src) {
		header = cat(text(string(p.src[c.Header.Start:c.Header.End])), lineHard)
	}
	lead := p.open(c)
	parts := p.stmtList(c.Stmts)
	ds := p.dangling()
	if len(c.Stmts) == 0 {
		ds = noBlankFirst(ds)
	}
	rest, _ := p.close()
	return cat(header, lead, catList(parts), catList(ds), rest)
}
