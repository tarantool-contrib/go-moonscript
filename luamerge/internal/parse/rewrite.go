package parse

import (
	"bytes"
	"regexp"
	"sort"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

// attribLen is the number of tokens of an attribute: "<", its name, ">".
const attribLen = 3

// keywords are the reserved words of Lua 5.2, those of Lua 5.1 and goto, each
// between spaces.
const keywords = " and break do else elseif end false for function goto if in local nil not or repeat" +
	" return then true until while "

// Keywords the rewriter and the token scanner look for in more than one
// place.
const (
	keywordEnd      = "end"
	keywordFunction = "function"
	keywordLocal    = "local"
)

// numeralRe matches the numeral at the start of the text of a Number token as
// the dialect that reads it takes it: a decimal or hexadecimal integer or
// float with its exponent, or a LuaJIT binary integer, and a LuaJIT suffix
// of a 64-bit integer (LL, ULL) or of an imaginary number (i). The
// alternatives are tried in order and every part is greedy, so it matches
// the longest numeral.
var numeralRe = regexp.MustCompile(`^(?:` +
	`0[xX](?:[0-9a-fA-F]+(?:\.[0-9a-fA-F]*)?|\.[0-9a-fA-F]+)(?:[pP][+-]?[0-9]+)?` +
	`|0[bB][01]+` +
	`|(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?` +
	`)(?:[iI]|[uU]?[lL][lL])?`)

// rewriter builds the copy of a Lua source that the fork's parser reads in
// its place when it refuses the source itself: the source with the syntax
// that LuaJIT, even with the syntax of LuaJIT's LJ_52 builds
// (parse.Options.Lua52Compat), does not have written as syntax it has, with
// the same calls (see rewrite). Every other byte stays, and every line break:
// a token is on its line in the copy, and the copy maps its offsets back to
// the source (see sourceCopy.offset).
type rewriter struct {
	src []byte
	// tokens are the tokens of src without its comments.
	tokens []lex.Token
	// edits are the changes to tokens that depend on the tokens around them,
	// by the index of the token.
	edits []edit
	// out is the copy so far.
	out []byte
	// moves are where the copy differs from the source, in order.
	moves []move
}

// edit is how the copy writes a token other than as it is.
type edit struct {
	// drop leaves the token out.
	drop bool
	// text, when not nil, is written in place of the token.
	text []byte
	// after, when not nil, is written after the token, on its line.
	after []byte
	// ends is set when the token ends the names of a declaration, so that a
	// statement can start after it, though its kind does not show that: the
	// "*" of "global *", or the ">" of an attribute.
	ends bool
}

// move is a part of the copy that differs from the source: out[Out:OutEnd]
// written in place of src[Src:SrcEnd].
type move struct {
	Out, OutEnd int
	Src, SrcEnd int
}

// sourceCopy is the copy of a source that the fork's parser reads, and how to
// find the source's offsets in it.
type sourceCopy struct {
	// source is the source, text the copy.
	source, text []byte
	// moves are where it differs from the source, in order.
	moves []move
	// tokens are the tokens of the source without its comments.
	tokens []lex.Token
}

// offset returns the offset in the source of the offset off of the copy: of
// the start of a span, or of its end when end is set. Inside a part of the
// copy written in place of other text, the start is the start of that text
// and the end its end.
func (c *sourceCopy) offset(off int, end bool) int {
	i := sort.Search(len(c.moves), func(i int) bool {
		if end {
			return c.moves[i].Out >= off
		}

		return c.moves[i].Out > off
	}) - 1
	if i < 0 {
		return off
	}

	part := c.moves[i]

	switch {
	case end && off <= part.OutEnd:
		return part.SrcEnd
	case !end && off < part.OutEnd:
		return part.Src
	default:
		return off - part.OutEnd + part.SrcEnd
	}
}

// isOpenParen reports whether tok, a token of the source, is "(".
func (c *sourceCopy) isOpenParen(tok lex.Token) bool {
	return tok.Kind == lex.Symbol && string(c.source[tok.Start:tok.End]) == "("
}

// rewrite returns the copy of src (see rewriter) whose tokens, without
// comments, are tokens: the tokens of src as lex.NewFile reads them. Only
// tokens change, each on its line, and the header that the interpreter skips
// when it loads a file (lex.Header) becomes spaces. The syntax that the fork
// does not read becomes syntax it reads, with the same calls:
//
//   - a string literal with an escape that only Lua 5.1 or Lua 5.4 read
//     becomes a literal of its value that LuaJIT reads (see stringValue),
//     followed by the line breaks it had;
//   - a numeral that LuaJIT does not end where another dialect does is
//     written as the tokens that dialect reads: "1e5..x" is "1e5 ..x" in Lua
//     5.1, and "3then" is "3 then" in Lua 5.2 and 5.3;
//   - the operators of Lua 5.3 become operators of Lua 5.1: "//" becomes
//     "/", "&", "|", "<<" and ">>" become "+", and "~", binary or unary,
//     becomes "-";
//   - the attributes of Lua 5.4 and 5.5 ("<const>", "<close>") are left out;
//   - a Lua 5.5 global declaration becomes a local declaration of the global
//     values of its names ("global a" is "local a = _ENV.a"), or the
//     function definition it declares, or "do end" for "global *";
//   - goto where it names a variable (Lua 5.1, LuaJIT) becomes another
//     name, as LJ_52 reads goto as a keyword everywhere;
//   - the syntax of LuaJIT 2.1 builds of 2026 becomes the Lua 5.1 syntax
//     with the same calls (see planLuaJIT and planContinue).
//
// What Lua 5.2 adds that LJ_52 reads stays as it is: empty statements, a
// statement after "break" and a call across a line break.
func rewrite(src []byte, tokens []lex.Token) *sourceCopy {
	r := &rewriter{
		src:    src,
		tokens: tokens,
		edits:  make([]edit, len(tokens)),
		out:    make([]byte, 0, len(src)+len(src)/8),
		moves:  nil,
	}

	for i := range r.tokens {
		r.plan(i)
	}

	header := lex.Header(src)
	r.out = append(r.out, bytes.Repeat([]byte(" "), header)...)
	pos := header

	for i, tok := range r.tokens {
		r.out = append(r.out, src[pos:tok.Start]...)
		r.emit(i, tok)
		pos = tok.End
	}

	r.out = append(r.out, src[pos:]...)

	return &sourceCopy{source: src, text: r.out, moves: r.moves, tokens: tokens}
}

// plan records the edits that the token at i and the tokens after it need
// because of what the token at i is.
func (r *rewriter) plan(i int) {
	switch r.name(i) {
	case keywordLocal:
		if r.name(i+1) != keywordFunction {
			r.dropAttrib(i + 1)
			r.nameList(r.attribEnd(i + 1))
		}
	case "global":
		r.planGlobal(i)
	case "goto":
		// goto starts a goto statement where a statement can start and a
		// label follows; anywhere else it is a name (Lua 5.1, LuaJIT).
		if !r.startsStatement(i) || !r.isIdentifier(i+1) {
			r.edits[i].text = []byte("_goto")
		}
	case "continue":
		r.planContinue(i)
	}

	r.planLuaJIT(i)
}

// doNothing is the statement of Lua 5.1 that does nothing, written in
// place of what does nothing in another dialect.
const doNothing = "do end"

// planGlobal records the edits of a Lua 5.5 global declaration that starts at
// i. global is a keyword only where it starts a statement and a declaration
// follows: a name, "function", an attribute or "*". Anywhere else it is a
// name, as in every other dialect: `local global = {}`, `global = 1`,
// `global < x`.
func (r *rewriter) planGlobal(i int) {
	if !r.startsStatement(i) {
		return
	}

	if r.name(i+1) == keywordFunction {
		// global function f() end defines the global f.
		r.edits[i].drop = true

		return
	}

	start := r.attribEnd(i + 1)

	switch {
	case r.isSymbol(start, "*"):
		// global *, global <const> *: free names are global, as they are
		// without a declaration.
		r.edits[i].text = []byte(doNothing)
		r.drop(i+1, start+1)
		r.edits[start].ends = true
	case r.isIdentifier(start):
		// global a, b [= x, y] declares a and b global for the rest of the
		// block, then assigns them: local a, b = _ENV.a, _ENV.b [a, b = x, y].
		r.drop(i+1, start)
		names, end := r.nameList(start)
		r.edits[i].text = []byte(keywordLocal)
		r.edits[end-1].after = r.globalValues(names, r.isSymbol(end, "="))
	}
}

// globalValues returns what follows the names of a global declaration,
// written as a local declaration: "= _ENV.a, _ENV.b" and, when the
// declaration assigns values, " a, b" for the "=" that follows to assign.
func (r *rewriter) globalValues(names []int, assigns bool) []byte {
	var text bytes.Buffer

	text.WriteString("=")

	for k, i := range names {
		if k > 0 {
			text.WriteString(",")
		}

		text.WriteString(" _ENV.")
		text.Write(r.nameText(i))
	}

	if assigns {
		for k, i := range names {
			if k > 0 {
				text.WriteString(",")
			}

			text.WriteString(" ")
			text.Write(r.nameText(i))
		}
	}

	return text.Bytes()
}

// nameText returns how the copy writes the name at i.
func (r *rewriter) nameText(i int) []byte {
	if r.edits[i].text != nil {
		return r.edits[i].text
	}

	return r.text(i)
}

// nameList reads the names of a declaration from i on, each with an optional
// attribute, `a <const>, b`, and drops the attributes. It returns the indexes
// of the names and the index after the list.
func (r *rewriter) nameList(i int) ([]int, int) {
	var names []int

	for r.isIdentifier(i) {
		names = append(names, i)
		r.dropAttrib(i + 1)
		i = r.attribEnd(i + 1)

		if !r.isSymbol(i, ",") {
			break
		}

		i++
	}

	if len(names) > 0 {
		r.edits[i-1].ends = true
	}

	return names, i
}

// attribEnd returns the index after the attribute "<name>" at i, or i when
// there is none there.
func (r *rewriter) attribEnd(i int) int {
	if r.isSymbol(i, "<") && r.isKind(i+1, lex.Name) && r.isSymbol(i+attribLen-1, ">") {
		return i + attribLen
	}

	return i
}

// dropAttrib drops the attribute at i, if there is one.
func (r *rewriter) dropAttrib(i int) {
	r.drop(i, r.attribEnd(i))
}

// drop drops the tokens from start up to end.
func (r *rewriter) drop(start, end int) {
	for i := start; i < end; i++ {
		r.edits[i].drop = true
	}
}

// startsStatement reports whether a statement can start at the token at i:
// at the start of the source, after a token that ends a statement or starts
// a block, and after a token that ends an expression, since two expressions
// never meet.
func (r *rewriter) startsStatement(i int) bool {
	if i == 0 || r.edits[i-1].ends {
		return true
	}

	prev := string(r.text(i - 1))

	switch r.tokens[i-1].Kind {
	case lex.Number, lex.String, lex.LongString:
		return true
	case lex.Name:
		switch prev {
		case "do", "then", "else", "repeat", keywordEnd, "break", "nil", "true", "false":
			return true
		default:
			// Any other keyword leads on to what follows it; a name ends
			// an expression.
			return !isKeyword(prev)
		}
	case lex.Symbol:
		switch prev {
		case ";", ")", "]", "}", "...", "::":
			return true
		default:
			return false
		}
	case lex.EOF, lex.Comment, lex.LongComment, lex.Invalid:
		return false
	}

	return false
}

// emit writes the copy of the token at i: as it is, unless it is edited, an
// operator of Lua 5.3 (see operator), or a string or a numeral that LuaJIT
// does not read. What is written in its place has a space before and after
// it, so that it reads as the tokens it is made of, and a dropped token
// leaves a space.
func (r *rewriter) emit(i int, tok lex.Token) {
	edit := r.edits[i]
	text := r.src[tok.Start:tok.End]

	switch {
	case edit.drop:
		r.substitute(tok.Start, tok.End, " ")
	case edit.text != nil:
		r.substitute(tok.Start, tok.End, " "+string(edit.text)+" ")
	case tok.NotLuaJIT && tok.Kind == lex.String:
		value := quote(stringValue(text, tok.Kind))
		r.substitute(tok.Start, tok.End, string(value)+strings.Repeat("\n", tok.EndLine-tok.Line))
	case tok.NotLuaJIT && tok.Kind == lex.Number:
		r.substitute(tok.Start, tok.End, string(numeral(text)))
	case tok.Kind == lex.Symbol && !bytes.Equal(operator(text), text):
		r.substitute(tok.Start, tok.End, " "+string(operator(text))+" ")
	default:
		r.out = append(r.out, text...)
	}

	if edit.after != nil {
		r.substitute(tok.End, tok.End, " "+string(edit.after)+" ")
	}
}

// substitute writes text in place of src[start:end].
func (r *rewriter) substitute(start, end int, text string) {
	out := len(r.out)
	r.out = append(r.out, text...)
	r.moves = append(r.moves, move{Out: out, OutEnd: len(r.out), Src: start, SrcEnd: end})
}

// numeral returns how the copy writes the Number token text, one that LuaJIT
// does not read: the numeral at its start as is when LuaJIT reads it and as 0
// when not, and the rest of the token, which the dialect that accepts it
// reads as tokens of their own, after a space. No numeral has ".." in it:
// Lua 5.1 ends "1e5..x" and "0x10..x" before it.
func numeral(text []byte) []byte {
	head, _, _ := bytes.Cut(text, []byte(".."))

	found := numeralRe.Find(head)
	if len(found) == 0 {
		// Cannot happen: a Number token starts with a digit, or with "."
		// and a digit, which numeralRe matches.
		return text
	}

	out := []byte("0")

	tokens, err := lex.New(found).Tokens()
	if err == nil && len(tokens) == 1 && !tokens[0].NotLuaJIT {
		out = bytes.Clone(found)
	}

	return append(append(out, ' '), text[len(found):]...)
}

// operator returns how the copy writes the Symbol token text: a Lua 5.3
// operator as an operator of Lua 5.1 with the same operands.
func operator(text []byte) []byte {
	switch string(text) {
	case "//":
		return []byte("/")
	case "&", "|", "<<", ">>":
		return []byte("+")
	case "~":
		// Binary or unary, as "-" is.
		return []byte("-")
	default:
		return text
	}
}

// text returns the text of the token at i.
func (r *rewriter) text(i int) []byte {
	return r.src[r.tokens[i].Start:r.tokens[i].End]
}

// isKind reports whether there is a token at i and it is of kind.
func (r *rewriter) isKind(i int, kind lex.Kind) bool {
	return i >= 0 && i < len(r.tokens) && r.tokens[i].Kind == kind
}

// name returns the text of the token at i when it is a Name, "" otherwise.
func (r *rewriter) name(i int) string {
	if !r.isKind(i, lex.Name) {
		return ""
	}

	return string(r.text(i))
}

// isIdentifier reports whether the token at i is a name that is no keyword.
func (r *rewriter) isIdentifier(i int) bool {
	name := r.name(i)

	return name != "" && !isKeyword(name)
}

// isSymbol reports whether the token at i is the symbol symbol.
func (r *rewriter) isSymbol(i int, symbol string) bool {
	return r.isKind(i, lex.Symbol) && string(r.text(i)) == symbol
}

// isKeyword reports whether name is a reserved word of Lua 5.2 (see keywords).
func isKeyword(name string) bool {
	return name != "" && strings.Contains(keywords, " "+name+" ")
}
