package ast

// go-moonscript patch: the chunk and its comments (see doc.go).

// Chunk is a parsed chunk, as parse.ParseChunk returns it. Its span is the
// whole source, [0, len(src)).
type Chunk struct {
	SpanBase

	// Name is the chunk name the parser was given.
	Name string
	// Stmts are the statements of the chunk, what parse.ParseBytes
	// returns; StmtsSpan is their block's span: from its first token to
	// its last (with the separators), or empty at the end of the source if
	// it has none.
	Stmts     []Stmt
	StmtsSpan Span
	// Header is what the lexer skipped before the first token as no Lua
	// text: LuaJIT's UTF-8 byte order mark and first line starting with
	// "#", or that line under Lua 5.1 with parse.Options.Shebang, without
	// its line break; the zero Span if none.
	Header Span
	// Comments are the comments of the source in order, if the parser was
	// asked for them (parse.Options.Comments).
	Comments []Comment
	// Lines gives the lines and columns of the offsets of the source.
	Lines *Lines
}

// Pos returns the position of the byte at offset (Lines.Pos).
func (c *Chunk) Pos(offset int) Pos {
	return c.Lines.Pos(offset)
}

// Positions returns the positions of the start and the end of the span
// of an element of the chunk (Lines.Positions).
func (c *Chunk) Positions(n Element) (start, end Pos) {
	return c.Lines.Positions(n.Span())
}

// Comment is a comment of the source.
type Comment struct {
	// Span is the comment from its "--" to its last byte: the end of the
	// line, before the line break, for a short comment; the closing
	// bracket for a long one.
	Span Span
	// Text is the comment without the "--" and, for a long comment, its
	// brackets, read as a long string is: without a line break right after
	// the opening bracket, each line break as "\n".
	Text string
	// Long is set for a long comment, "--[[ ... ]]" or "--[==[ ... ]==]".
	Long bool
}
