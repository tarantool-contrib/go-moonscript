package parse

import (
	"bytes"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/tarantool/go-luamerge/internal/lex"
)

// attribLen is the number of tokens of an attribute: "<", its name, ">".
const attribLen = 3

// keywords are the reserved words of Lua 5.2, those of Lua 5.1 and goto, each
// between spaces.
const keywords = " and break do else elseif end false for function goto if in local nil not or repeat" +
	" return then true until while "

// Keywords the normalizer and the token scanner look for in more than one
// place.
const (
	keywordEnd      = "end"
	keywordFunction = "function"
)

// numeralRe matches the numeral at the start of the text of a Number token as
// the dialect that reads it takes it: a decimal or hexadecimal integer or
// float with its exponent (hexadecimal floats are Lua 5.2 and LuaJIT), or a
// LuaJIT binary integer, and a LuaJIT suffix of a 64-bit integer (LL, ULL)
// or of an imaginary number (i). The alternatives are tried in order and
// every part is greedy, so it matches the longest numeral.
var numeralRe = regexp.MustCompile(`^(?:` +
	`0[xX](?:[0-9a-fA-F]+(?:\.[0-9a-fA-F]*)?|\.[0-9a-fA-F]+)(?:[pP][+-]?[0-9]+)?` +
	`|0[bB][01]+` +
	`|(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?` +
	`)(?:[iI]|[uU]?[lL][lL])?`)

// gopherNumeralRe matches a numeral gopher-lua reads: a decimal integer or
// float with its exponent, or a hexadecimal integer.
var gopherNumeralRe = regexp.MustCompile(`^(?:0[xX][0-9a-fA-F]+|(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)$`)

// normalizer builds the copy of a Lua source that gopher-lua, a parser of Lua
// 5.1, reads in its place: the tokens of the source in their order and on
// their lines, each written the way Lua 5.1 reads it, with only spaces and
// line breaks between them. The copy is read for its calls of require only:
// the bundle embeds the source itself.
type normalizer struct {
	src []byte
	// tokens are the tokens of src without its comments.
	tokens []lex.Token
	// edits are the changes to tokens that depend on the tokens around them,
	// by the index of the token.
	edits []edit
	// out is the copy so far.
	out []byte
	// line is the line the end of out is on.
	line int
}

// edit is how the copy writes a token other than by its kind alone.
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

// normalize returns the copy of src that gopher-lua reads. Every token is on
// the line it is on in src, so gopher-lua's lines are the lines of src.
// Comments are left out, and only spaces and line breaks separate the
// tokens, as gopher-lua knows no form feed or vertical tab. The syntax that
// Lua 5.1 does not have becomes syntax it has, with the same calls:
//
//   - a string literal, which may use escapes Lua 5.1 does not have or span
//     lines, becomes a literal of one line that Lua 5.1 reads as its value
//     (see stringValue);
//   - the operators of Lua 5.3 become operators of Lua 5.1: "//" becomes
//     "/", "&", "|", "<<" and ">>" become "+", and "~", binary or unary,
//     becomes "-";
//   - the attributes of Lua 5.4 and 5.5 ("<const>", "<close>") are left out;
//   - a numeral gopher-lua does not read (a hexadecimal float, a LuaJIT
//     binary integer, 64-bit integer or imaginary number) becomes 0, and the
//     rest of a numeral token that the dialect which accepts it reads as
//     tokens of their own is written as those tokens: "1e5..x" is "1e5 .. x"
//     in Lua 5.1, and "3then" is "3 then" in Lua 5.2 and 5.3;
//   - a Lua 5.5 global declaration becomes a local declaration of the global
//     values of its names ("global a" is "local a = _ENV.a"), or the
//     function definition it declares, or ";" for "global *";
//   - a "break" before the end of its block, which Lua 5.2 allows, becomes
//     "do break end";
//   - a name with bytes beyond ASCII (LuaJIT) becomes an ASCII name, and
//     goto where it names a variable (Lua 5.1, LuaJIT) another name.
//
// src is read the way the interpreter reads a file it loads (see
// significantTokens). The error is a lexical error, of source no dialect
// reads.
func normalize(src []byte) ([]byte, error) {
	tokens, err := significantTokens(src)
	if err != nil {
		return nil, err
	}

	return normalizeTokens(src, tokens), nil
}

// significantTokens returns the tokens of src without its comments, read the
// way the interpreter reads a file it loads (see lex.NewFile). The error is a
// lexical error, of source no dialect reads.
func significantTokens(src []byte) ([]lex.Token, error) {
	all, err := lex.NewFile(src).Tokens()
	if err != nil {
		return nil, err //nolint:wrapcheck // a *lex.Error, which says what went wrong and where.
	}

	tokens := make([]lex.Token, 0, len(all))

	for _, tok := range all {
		if tok.Kind != lex.Comment && tok.Kind != lex.LongComment {
			tokens = append(tokens, tok)
		}
	}

	return tokens, nil
}

// normalizeTokens returns the copy of src (see normalize) whose tokens,
// without comments, are tokens.
func normalizeTokens(src []byte, tokens []lex.Token) []byte {
	n := &normalizer{
		src:    src,
		tokens: tokens,
		edits:  make([]edit, len(tokens)),
		out:    make([]byte, 0, len(src)),
		line:   1,
	}

	for i := range n.tokens {
		n.plan(i)
	}

	for i, tok := range n.tokens {
		n.emit(i, tok)
	}

	return n.out
}

// plan records the edits that the token at i and the tokens after it need
// because of what the token at i is.
func (n *normalizer) plan(i int) {
	switch n.name(i) {
	case "local":
		if n.name(i+1) != keywordFunction {
			n.dropAttrib(i + 1)
			n.nameList(n.attribEnd(i + 1))
		}
	case "global":
		n.planGlobal(i)
	case "goto":
		// goto starts a goto statement where a statement can start and a
		// label follows; anywhere else it is a name (Lua 5.1, LuaJIT).
		if !n.startsStatement(i) || !n.isIdentifier(i+1) {
			n.edits[i].text = []byte("_goto")
		}
	case "break":
		if !n.endsBlock(i + 1) {
			n.edits[i].text = []byte("do break end")
		}
	}
}

// endsBlock reports whether the block ends at i, after the semicolons there:
// at "end", "else", "elseif", "until", or at the end of the source.
func (n *normalizer) endsBlock(i int) bool {
	for n.isSymbol(i, ";") {
		i++
	}

	switch n.name(i) {
	case keywordEnd, "else", "elseif", "until":
		return true
	default:
		return i == len(n.tokens)
	}
}

// planGlobal records the edits of a Lua 5.5 global declaration that starts at
// i. global is a keyword only where it starts a statement and a declaration
// follows: a name, "function", an attribute or "*". Anywhere else it is a
// name, as in every other dialect: `local global = {}`, `global = 1`,
// `global < x`.
func (n *normalizer) planGlobal(i int) {
	if !n.startsStatement(i) {
		return
	}

	if n.name(i+1) == keywordFunction {
		// global function f() end defines the global f.
		n.edits[i].drop = true

		return
	}

	start := n.attribEnd(i + 1)

	switch {
	case n.isSymbol(start, "*"):
		// global *, global <const> *: free names are global, as they are
		// without a declaration.
		n.edits[i].text = []byte(";")
		n.drop(i+1, start+1)
		n.edits[start].ends = true
	case n.isIdentifier(start):
		// global a, b [= x, y] declares a and b global for the rest of the
		// block, then assigns them: local a, b = _ENV.a, _ENV.b [a, b = x, y].
		n.drop(i+1, start)
		names, end := n.nameList(start)
		n.edits[i].text = []byte("local")
		n.edits[end-1].after = n.globalValues(names, n.isSymbol(end, "="))
	}
}

// globalValues returns what follows the names of a global declaration,
// written as a local declaration: "= _ENV.a, _ENV.b" and, when the
// declaration assigns values, " a, b" for the "=" that follows to assign.
func (n *normalizer) globalValues(names []int, assigns bool) []byte {
	var text bytes.Buffer

	text.WriteString("=")

	for k, i := range names {
		if k > 0 {
			text.WriteString(",")
		}

		text.WriteString(" _ENV.")
		text.Write(n.copyOf(lex.Name, n.text(i)))
	}

	if assigns {
		for k, i := range names {
			if k > 0 {
				text.WriteString(",")
			}

			text.WriteString(" ")
			text.Write(n.copyOf(lex.Name, n.text(i)))
		}
	}

	return text.Bytes()
}

// nameList reads the names of a declaration from i on, each with an optional
// attribute, `a <const>, b`, and drops the attributes. It returns the indexes
// of the names and the index after the list.
func (n *normalizer) nameList(i int) ([]int, int) {
	var names []int

	for n.isIdentifier(i) {
		names = append(names, i)
		n.dropAttrib(i + 1)
		i = n.attribEnd(i + 1)

		if !n.isSymbol(i, ",") {
			break
		}

		i++
	}

	if len(names) > 0 {
		n.edits[i-1].ends = true
	}

	return names, i
}

// attribEnd returns the index after the attribute "<name>" at i, or i when
// there is none there.
func (n *normalizer) attribEnd(i int) int {
	if n.isSymbol(i, "<") && n.isKind(i+1, lex.Name) && n.isSymbol(i+attribLen-1, ">") {
		return i + attribLen
	}

	return i
}

// dropAttrib drops the attribute at i, if there is one.
func (n *normalizer) dropAttrib(i int) {
	n.drop(i, n.attribEnd(i))
}

// drop drops the tokens from start up to end.
func (n *normalizer) drop(start, end int) {
	for i := start; i < end; i++ {
		n.edits[i].drop = true
	}
}

// startsStatement reports whether a statement can start at the token at i:
// at the start of the source, after a token that ends a statement or starts
// a block, and after a token that ends an expression, since two expressions
// never meet.
func (n *normalizer) startsStatement(i int) bool {
	if i == 0 || n.edits[i-1].ends {
		return true
	}

	prev := string(n.text(i - 1))

	switch n.tokens[i-1].Kind {
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

// emit writes the copy of the token at i.
func (n *normalizer) emit(i int, tok lex.Token) {
	edit := n.edits[i]

	if !edit.drop {
		text := edit.text
		if text == nil {
			text = n.copyOf(tok.Kind, n.text(i))
		}

		n.write(tok.Line, text)
	}

	if edit.after != nil {
		n.write(tok.Line, edit.after)
	}
}

// copyOf returns how the copy writes a token of kind whose text is text, by
// its kind alone.
func (n *normalizer) copyOf(kind lex.Kind, text []byte) []byte {
	switch kind {
	case lex.String, lex.LongString:
		return quote(stringValue(text, kind))
	case lex.Number:
		return n.numeral(text)
	case lex.Name:
		return asciiName(text)
	case lex.Symbol:
		return operator(text)
	case lex.EOF, lex.Comment, lex.LongComment, lex.Invalid:
		return text
	}

	return text
}

// numeral returns how the copy writes the Number token text: its numeral as
// is when gopher-lua reads it, as 0 when not, and the rest of the token, which
// the dialect that accepts it reads as tokens of their own, as those tokens.
// No numeral has ".." in it: Lua 5.1 ends "1e5..x" and "0x10..x" before it.
func (n *normalizer) numeral(text []byte) []byte {
	numeral, _, _ := bytes.Cut(text, []byte(".."))

	numeral = numeralRe.Find(numeral)
	if len(numeral) == 0 {
		// Cannot happen: a Number token starts with a digit, or with "."
		// and a digit, which numeralRe matches.
		return text
	}

	out := []byte("0")
	if gopherNumeralRe.Match(numeral) {
		out = bytes.Clone(numeral)
	}

	rest := text[len(numeral):]

	tokens, err := lex.New(rest).Tokens()
	if err != nil {
		// Cannot happen: the rest of a Number token holds name characters,
		// dots and a sign after an exponent letter, which lex without error.
		return text
	}

	for _, tok := range tokens {
		out = append(out, ' ')
		out = append(out, n.copyOf(tok.Kind, rest[tok.Start:tok.End])...)
	}

	return out
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

// asciiName returns how the copy writes the Name token text: a name with bytes
// beyond ASCII, which LuaJIT allows, as "_" and the hexadecimal digits of its
// bytes, a name no other name of the copy is.
func asciiName(text []byte) []byte {
	for _, b := range text {
		if b >= utf8.RuneSelf {
			return []byte("_" + hex.EncodeToString(text))
		}
	}

	return text
}

// write writes text as a token on line: after the line breaks that take the
// copy to line, or after a space on the line the copy is on.
func (n *normalizer) write(line int, text []byte) {
	switch {
	case n.line < line:
		n.out = append(n.out, bytes.Repeat([]byte("\n"), line-n.line)...)
		n.line = line
	case len(n.out) > 0:
		n.out = append(n.out, ' ')
	}

	n.out = append(n.out, text...)
}

// text returns the text of the token at i.
func (n *normalizer) text(i int) []byte {
	return n.src[n.tokens[i].Start:n.tokens[i].End]
}

// isKind reports whether there is a token at i and it is of kind.
func (n *normalizer) isKind(i int, kind lex.Kind) bool {
	return i < len(n.tokens) && n.tokens[i].Kind == kind
}

// name returns the text of the token at i when it is a Name, "" otherwise.
func (n *normalizer) name(i int) string {
	if !n.isKind(i, lex.Name) {
		return ""
	}

	return string(n.text(i))
}

// isIdentifier reports whether the token at i is a name that is no keyword.
func (n *normalizer) isIdentifier(i int) bool {
	name := n.name(i)

	return name != "" && !isKeyword(name)
}

// isSymbol reports whether the token at i is the symbol symbol.
func (n *normalizer) isSymbol(i int, symbol string) bool {
	return n.isKind(i, lex.Symbol) && string(n.text(i)) == symbol
}

// isKeyword reports whether name is a reserved word of Lua 5.2 (see keywords).
func isKeyword(name string) bool {
	return name != "" && strings.Contains(keywords, " "+name+" ")
}
