// Package ast declares the syntax tree of Lua chunks that package parse
// builds and that the compiler (lua.CompileAST) compiles.
//
// # Lines
//
// Every statement and expression has the lines the compiler gives its
// instructions (PositionHolder): Line, and for some nodes LastLine, as
// luac and LuaJIT number them. They are not always where the node's text
// starts: a parenthesized expression is on the line of its "(", a unary
// operation on that of its operand, the body of a function statement on
// that of its "(". Compiling a tree reads only these lines, and they are
// what they were before positions were added.
//
// # Spans and positions (go-moonscript patch)
//
// Every element of a tree (Element: the statements, the expressions, the
// table fields, parameter lists, function names and the chunk) has a
// Span: the byte offsets [Start, End) of its text in the source, from the
// start of its first token to the end of its last one, so that
// src[n.Span().Start:n.Span().End] is the node's text. A token that spans
// lines (a long string, a string with escaped line breaks or "\z") is
// whole in it. What is in a node's span:
//
//   - a statement: from its first keyword or expression to its last token,
//     without the ";" after it (that is in its block's span);
//   - an expression: its own tokens, without the parentheses around it,
//     which are in Parens (below); an expression holding a parenthesized
//     one spans its parentheses: the span of "(a).b" starts at the "(";
//   - a name used as a string (the key of a.b, of {b = 1}, a name in
//     function a.b()): a StringExpr spanning the name; a string literal's
//     span starts with its quote or long bracket, a name's does not;
//   - an "elseif": the IfStmt in the Else of the one before, from the
//     "elseif" to the last token before the "end", which belongs to the
//     first IfStmt;
//   - a function: from "function" for an expression, from the "(" of the
//     parameters for the body of a function statement (FuncDefStmt and
//     "local function", whose statement spans from "function" or
//     "local"), to the "end"; its ParList from "(" to ")";
//   - a table field: the item without the separator after it.
//
// Blocks are lists of statements ([]Stmt), not elements: the nodes holding
// them have their spans next to them (StmtsSpan, ThenSpan, ElseSpan,
// Chunk.StmtsSpan), from the block's first token to its last, with the
// separators; an empty block is empty at the token after it. Names held
// as strings have their spans next to them too: NameSpans for the names
// of a local statement, of a generic for and the parameters,
// NameSpan for a numeric for's variable and a label, LabelSpan for a goto,
// MethodSpan for a method call's and a method's name, VarargSpan for a
// "..." parameter. A slice of spans is nil in a tree built by other code.
//
// A Pos is a position: the byte offset, the 1-based line and the 1-based
// column. Columns count bytes, as go-luascope's do: a tab or a byte of a
// UTF-8 character (a LuaJIT identifier may hold bytes 128-255) is one
// column. Lines are counted as the lexer counts them: "\n", "\r", "\r\n"
// and "\n\r" each end one line. Nodes keep offsets only; Lines turns an
// offset into a Pos, from a table of the starts of the lines that it
// builds on first use (Chunk.Lines, or NewLines for a tree from
// parse.ParseBytes). This keeps a node 8 bytes bigger rather than 48 for
// a start and an end Pos: the lexer counts every line break the same way,
// wherever it is (between tokens, in a comment, in a long string, after a
// "\" or a "\z" in a string), so the line of an offset depends on the
// source only and is the line the lexer gives the token there. Offsets
// are 32-bit: the elements of a source of 4 GiB or more have the zero
// Span.
//
// A node without a position, built by other code, has the zero Span, and
// the compiler doesn't read spans: a tree with or without them compiles to
// the same function (see lua.CompileAST for the lines it needs).
//
// # Parentheses
//
// Parentheses are not nodes: (f()) is the FuncCallExpr f(), its Parens
// telling that it is parenthesized: how many pairs (((x)) has 2) and the
// span of the outermost pair, also its OuterSpan. They truncate a call or
// "..." to one value, which the compiler takes from FuncCallExpr.AdjustRet
// and Comma3Expr.AdjustRet, as before; Parens are for tools.
//
// # Comments
//
// parse.ParseChunk with parse.Options.Comments keeps the comments in
// Chunk.Comments, in source order, as go-luascope does: a list on the
// chunk, not attached to nodes. Collecting them costs a string each, and
// nothing when they are not asked for. The first line that LuaJIT's lexer
// skips (a "#" line, after an optional UTF-8 byte order mark), or that
// lua5.1 skips running a file (parse.Options.Shebang), is no comment: it
// is Chunk.Header.
//
// # Walking a tree
//
// Walk and Inspect traverse a tree in source order, as go/ast's do;
// Inspect, like go-luascope's, doesn't call its function with nil after
// the children.
//
// # Mapping to go-luascope and go-luamerge
//
// go-luascope's ast.Pos{Line, Col} is Pos's Line and Column, counted the
// same way; its node's Pos() is Lines.Pos(n.Span().Start) here. Its
// Comment{Pos, Text, Long} is Comment's Span.Start, Text (the same text:
// without "--" and brackets, a long comment's line breaks as "\n") and
// Long. Its Paren node is Expr.Parens here, its Block a []Stmt with its
// span, its Local the names with their spans. go-luamerge's parse.Span
// {Start, End} is Span: the literal of require("x") is the Span of the
// StringExpr argument, also inside parentheses, require(("x")).
package ast
