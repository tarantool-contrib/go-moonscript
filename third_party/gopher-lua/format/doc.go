// Package format prints Lua 5.1 and LuaJIT syntax trees as source and
// formats sources: a printer for trees that other code built or
// transformed, and a formatter, gofmt's kind, that keeps what the author
// of a source chose. go-moonscript patch.
//
//	out, err := format.Source(src, format.Options{Dialect: parse.LuaJIT})
//
//	chunk, err := parse.ParseChunk(src, name, parse.Options{Tokens: true})
//	cm := format.NewCommentMap(chunk) // the comments, attached to elements
//	// ... transform chunk ...
//	err = format.Chunk(w, chunk, cm, opts)
//
//	err = format.Node(w, tree, opts) // a built tree, canonical
//
// # Correctness
//
// What the printer and the formatter write parses, in the dialect of the
// options, to a tree that compiles to the same code as the tree printed
// (the instructions, constants, functions, upvalues, locals and registers;
// only the lines differ): the tests check it over the compatibility
// scripts, the fork's test scripts and the luasuites archives in both
// dialects, and fuzz it. Formatting a formatted source changes nothing,
// and the formatter keeps the comments of the source as written and in
// order, and its string and numeral tokens.
//
// To that end the printer writes:
//
//   - the parentheses that precedence, associativity and the grammar need
//     (operands, a prefix expression that is not one: ("s"):rep(2), (f or
//     g)(), ({}).k), and those of a call or "..." truncated to one value
//     (FuncCallExpr.AdjustRet, Comma3Expr.AdjustRet) at the end of an
//     expression list, an argument list or a table, where the truncation
//     matters; the formatter keeps those of the source as well;
//   - a.b only if b is a name of the dialect ("goto" is a keyword of
//     LuaJIT), else a["b"];
//   - strings with escapes that read back as the same bytes in both
//     dialects (decimal escapes; valid UTF-8 as is), numerals that read
//     back as the same number (the infinity as 1e999, a negative value with
//     a unary minus, NaN as 0 / 0, which the compiler folds);
//   - no line break before the "(" of arguments ("f\n(g)" is Lua's
//     ambiguous syntax: comments that go there are written after the "("),
//     a ";" before a statement that starts with "(", a space between two
//     tokens that would make one ("- -x", "a .. .5", "1 .. 2", "t[ [[s]]]").
//
// # Comments
//
// A CommentMap attaches each comment of a source to an element of its
// tree (NewCommentMap has the rules): Leading, on the lines above it or
// before it on its line; Trailing, after it on the line it ends on; Inner,
// in an element where no child is (an empty block, table or list, the end
// of a block, before a keyword). The printer writes each comment of the map
// once, with its element: leading ones before it, trailing ones after it
// (after the "," or ";" that follows it), inner ones before the token of
// the element they were before, or at the end of the element. A short
// comment ends its line. So comments follow their elements when a
// transformation moves them; those of elements no longer in the tree are an
// error of Chunk, not lost silently.
//
// # Style
//
// The style is the package's own. Statements are one a line, blocks
// indented one level (4 spaces, IndentWidth, or tabs), binary operators,
// "=" and "," spaced, calls and indexing tight: f(a, b), t[k], {a = 1, 2}.
// What does not fit in the width (Width, 100 columns) is broken over lines,
// as Wadler's and Oppen's pretty printers do (the variant of Prettier: a
// group is laid out flat if it fits up to the next possible line break,
// else broken):
//
//   - arguments, parameters and table fields one a line, indented, the
//     closing bracket on a line of its own; a call whose last argument is a
//     function or a table opens it on the line of the call and closes the
//     call on the line of its end, if the line fits: f(a, function()
//     ... end), setmetatable(t, { ... });
//   - a chain of binary operators of the same precedence breaks after its
//     operators, its operands indented one level (two in the condition of
//     an if or a while, not to align with the block);
//   - a list of values or targets breaks after its commas.
//
// The formatter keeps the author's choices (Chunk): the text of literals
// (the quotes change only with Quotes), a.b or a["b"], f"s" and f{...},
// parentheses, "else if", the empty lines between statements and between
// table fields (one of several), one-line blocks and functions with at
// most a statement in each block (if they fit), tables with a line break
// after their "{" (laid out on lines). Options.TrailingComma ends the
// tables laid out on lines with a ","; without it, they keep the trailing
// separator of the source. A table's separators are written ",".
// Options.KeepSemicolons keeps the ";" after statements; else they go.
//
// # Limits
//
// A built tree is printed so that it parses back to the same tree, but
// for what Lua cannot write: a vararg function without "arg" under Lua 5.1
// (ParList.NoArg) reads back with it, a NumberExpr's Value reads back as
// the parser of the dialect writes numerals, an expression's lines are
// those of its new text, parentheses that change nothing are dropped. The
// LuaJIT parser emits code to check LuaJIT's limits (registers, constants):
// a source at those limits can be one the formatter's parentheses or the
// printer's lack of them move over the limit; the tests found none.
package format
