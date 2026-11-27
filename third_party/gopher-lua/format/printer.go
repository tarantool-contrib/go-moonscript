package format

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
)

// printer builds the document of a tree. With a source (src and toks: the
// tree was parsed from it, with its tokens), it keeps what the author chose
// for the elements that have a span in it: the text of literals, the form
// of keys and calls, the parentheses, the blank lines, one-line blocks and
// broken tables. Without, or for an element without a span, it uses the
// canonical forms. It prints the comments of cm with their elements.
type printer struct {
	opts  Options
	jit   bool
	src   []byte
	toks  ast.Tokens
	lines *ast.Lines

	cm      CommentMap
	done    map[*Comment]bool
	emitted int
	frames  []frame
	// noBlank: the next comment opens a block, without an empty line
	// before it
	noBlank bool
	depth   int
}

// frame is an element being printed: its inner comments not printed yet
// and the number of its children printed.
type frame struct {
	el    ast.Element
	inner []*Comment
	n     int
}

type printError struct{ err error }

func (p *printer) fail(format string, args ...any) {
	panic(printError{fmt.Errorf("format: "+format, args...)})
}

const maxDepth = 10000

func (p *printer) enter() {
	p.depth++
	if p.depth > maxDepth {
		p.fail("tree too deep")
	}
}

func (p *printer) leave() { p.depth-- }

/* source */

// fromSource reports whether el has a span in the printer's source.
func (p *printer) fromSource(el ast.Element) bool {
	if p.src == nil {
		return false
	}
	s := el.Span()
	return s != (ast.Span{}) && s.End <= len(p.src) && s.Start < s.End
}

// tokenAt returns the index of the token starting at off, or -1.
func (p *printer) tokenAt(off int) int {
	i := p.toks.At(off)
	if i < 0 || p.toks[i].Span().Start != off {
		return -1
	}
	return i
}

// kindAt returns the kind of the token starting at off, TokenInvalid if
// none does.
func (p *printer) kindAt(off int) ast.TokenKind {
	if i := p.tokenAt(off); i >= 0 {
		return p.toks[i].Kind
	}
	return ast.TokenInvalid
}

// raw returns the text of the token of the kind that the span of el is,
// or "".
func (p *printer) raw(el ast.Element, kind ast.TokenKind) string {
	if !p.fromSource(el) {
		return ""
	}
	s := el.Span()
	i := p.tokenAt(s.Start)
	if i < 0 || p.toks[i].Kind != kind || p.toks[i].Span() != s {
		return ""
	}
	return string(p.src[s.Start:s.End])
}

// nextKind returns the kind of the first token of the grammar at or after
// off.
func (p *printer) nextKind(off int) ast.TokenKind {
	i := p.toks.At(off)
	if i < 0 {
		return ast.TokenInvalid
	}
	for ; i < len(p.toks); i++ {
		if !p.toks[i].Kind.IsTrivia() {
			return p.toks[i].Kind
		}
	}
	return ast.TokenInvalid
}

// prevKind returns the kind of the last token of the grammar before off.
func (p *printer) prevKind(off int) ast.TokenKind {
	i := p.toks.At(off)
	if i < 0 {
		return ast.TokenInvalid
	}
	for i--; i >= 0; i-- {
		if !p.toks[i].Kind.IsTrivia() {
			return p.toks[i].Kind
		}
	}
	return ast.TokenInvalid
}

// blankBefore reports whether an empty line is right before el in the
// source: two line breaks with only white space between them and el.
func (p *printer) blankBefore(el ast.Element) bool {
	if !p.fromSource(el) {
		return false
	}
	i := p.tokenAt(outerSpan(el).Start)
	if i < 0 {
		return false
	}
	nl := 0
	for i--; i >= 0; i-- {
		switch p.toks[i].Kind {
		case ast.TokenNewline:
			nl++
		case ast.TokenWhitespace:
		default:
			return nl >= 2
		}
	}
	return false
}

// newlineAfter reports whether a line break follows the token starting at
// off before the next token that is not white space.
func (p *printer) newlineAfter(off int) bool {
	i := p.tokenAt(off)
	if i < 0 {
		return false
	}
	for i++; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case ast.TokenNewline:
			return true
		case ast.TokenWhitespace:
		default:
			return false
		}
	}
	return false
}

// oneLine reports whether el, holding blocks, is on one line of the
// source with at most one statement in each block: it is laid out on one
// line if it fits.
func (p *printer) oneLine(el ast.Element, blocks ...[]ast.Stmt) bool {
	if !p.fromSource(el) {
		return false
	}
	for _, b := range blocks {
		if len(b) > 1 {
			return false
		}
	}
	s := el.Span()
	return p.lines.Pos(s.Start).Line == p.lines.Pos(s.End).Line
}

/* comments */

func (p *printer) commentDoc(c *Comment) doc {
	if p.done[c] {
		return nil
	}
	if p.done == nil {
		p.done = map[*Comment]bool{}
	}
	p.done[c] = true
	p.emitted++
	raw := c.Raw
	if raw == "" {
		raw = commentRaw(c.Text, c.Long)
	}
	long := strings.HasPrefix(raw, "--") && ast.LongBracketLevel([]byte(raw[2:])) >= 0
	d := &comment{raw: raw, short: !long, sameLine: c.SameLine, blank: c.BlankBefore && !p.noBlank, nlAft: c.NewlineAfter}
	p.noBlank = false
	return d
}

// commentRaw writes a comment of the text: short if it fits on a line,
// else long, with a long bracket that the text does not close.
func commentRaw(t string, long bool) string {
	if !long && firstLineBreak(t) < 0 {
		if ast.LongBracketLevel([]byte(t)) >= 0 {
			return "-- " + t
		}
		return "--" + t
	}
	level := 0
	for strings.Contains(t+"]", "]"+strings.Repeat("=", level)+"]") {
		level++
	}
	eq := strings.Repeat("=", level)
	nl := ""
	if t != "" && (t[0] == '\n' || t[0] == '\r') {
		nl = "\n"
	}
	return "--[" + eq + "[" + nl + t + "]" + eq + "]"
}

// open starts printing el: it returns the inner comments of the element
// being printed that go before el and el's leading comments, and makes
// el the element being printed.
func (p *printer) open(el ast.Element) doc {
	if p.cm == nil {
		return nil
	}
	var out []doc
	if len(p.frames) > 0 {
		f := &p.frames[len(p.frames)-1]
		out = p.flush(f, out, func(c *Comment) bool { return c.Index <= f.n })
	}
	var inner []*Comment
	for _, c := range p.cm[el] {
		switch c.Place {
		case Leading:
			if d := p.commentDoc(c); d != nil {
				out = append(out, d)
			}
		case Inner:
			if !p.done[c] {
				inner = append(inner, c)
			}
		}
	}
	p.frames = append(p.frames, frame{el: el, inner: inner})
	return catList(out)
}

// close ends printing an element: it returns its inner comments not
// printed yet and its trailing comments.
func (p *printer) close() (rest, trail doc) {
	if p.cm == nil {
		return nil, nil
	}
	f := &p.frames[len(p.frames)-1]
	r := p.flush(f, nil, func(*Comment) bool { return true })
	el := f.el
	p.frames = p.frames[:len(p.frames)-1]
	if len(p.frames) > 0 {
		p.frames[len(p.frames)-1].n++
	}
	var t []doc
	for _, c := range p.cm[el] {
		if c.Place == Trailing {
			if d := p.commentDoc(c); d != nil {
				t = append(t, d)
			}
		}
	}
	return catList(r), catList(t)
}

// flush appends to out the docs of the first inner comments of f that ok
// accepts.
func (p *printer) flush(f *frame, out []doc, ok func(*Comment) bool) []doc {
	i := 0
	for i < len(f.inner) && ok(f.inner[i]) {
		if d := p.commentDoc(f.inner[i]); d != nil {
			out = append(out, d)
		}
		i++
	}
	f.inner = f.inner[i:]
	return out
}

// before returns the inner comments of the element being printed that go
// before its own token of one of the kinds.
func (p *printer) before(kinds ...ast.TokenKind) doc {
	return catList(p.beforeDocs(kinds...))
}

// beforeDocs is before, a list.
func (p *printer) beforeDocs(kinds ...ast.TokenKind) []doc {
	if p.cm == nil || len(p.frames) == 0 {
		return nil
	}
	f := &p.frames[len(p.frames)-1]
	if len(f.inner) == 0 {
		return nil
	}
	return p.flush(f, nil, func(c *Comment) bool {
		if c.Index < f.n {
			return true
		}
		if c.Index > f.n {
			return false
		}
		for _, k := range kinds {
			if c.Next == k {
				return true
			}
		}
		return false
	})
}

// tok is an own token of the element being printed, after the inner
// comments that go before it.
func (p *printer) tok(kind ast.TokenKind, s string) doc {
	return cat(p.before(kind), text(s))
}

// dangling returns the inner comments of the element being printed that
// go in an empty list or at the end of a block: all those not printed yet
// that are after its children printed.
func (p *printer) dangling() []doc {
	if p.cm == nil || len(p.frames) == 0 {
		return nil
	}
	f := &p.frames[len(p.frames)-1]
	return p.flush(f, nil, func(c *Comment) bool { return c.Index <= f.n })
}

// hasLeading reports whether el has leading comments.
func (p *printer) hasLeading(el ast.Element) bool {
	for _, c := range p.cm[el] {
		if c.Place == Leading {
			return true
		}
	}
	return false
}

// sameLineLead takes the leading comments of el that are on the line of
// the token before it: to print them on the line of the token that opens
// a block or a list.
func (p *printer) sameLineLead(el ast.Element) doc {
	if p.cm == nil || isNil(el) {
		return nil
	}
	var out []doc
	for _, c := range p.cm[el] {
		if c.Place != Leading || !c.SameLine || !(c.NewlineAfter || !c.Long) {
			break
		}
		if d := p.commentDoc(c); d != nil {
			out = append(out, d)
		}
	}
	return catList(out)
}

// dangle lays out comments in an empty list: those on the line of open
// after it, the others indented on lines of their own.
func dangle(open doc, ds []doc, closeTok doc) doc {
	pre, rest := splitSameLine(noBlankFirst(ds))
	if len(rest) == 0 {
		if isHard(pre) {
			return cat(open, pre, lineHard, closeTok)
		}
		return cat(open, pre, closeTok)
	}
	return cat(open, pre, mkNest(cat(lineHard, catList(rest))), lineHard, closeTok)
}

// noBlankFirst drops the empty line before the first of the comments
// opening a block or a list.
func noBlankFirst(ds []doc) []doc {
	for _, d := range ds {
		if c, ok := d.(*comment); ok {
			c.blank = false
			break
		}
	}
	return ds
}

// splitSameLine returns the docs of the comments at the start of ds that
// are on the line of the token before them, and the rest.
func splitSameLine(ds []doc) (doc, []doc) {
	i := 0
	for i < len(ds) {
		c, ok := ds[i].(*comment)
		if !ok || !c.sameLine {
			break
		}
		i++
	}
	return catList(ds[:i]), ds[i:]
}

/* expressions */

// precedence levels: an operand needs parentheses if its level is lower
// than its context's
const (
	precOr     = 1
	precAnd    = 2
	precCmp    = 3
	precConcat = 4 // right associative
	precAdd    = 5
	precMul    = 6
	precUnary  = 7
	precPow    = 8 // right associative; its right operand may be unary
	precSimple = 9 // literals, tables, functions, "..."
	precPrefix = 10
)

func (p *printer) prec(e ast.Expr) int {
	switch e := e.(type) {
	case *ast.LogicalOpExpr:
		if e.Operator == "or" {
			return precOr
		}
		return precAnd
	case *ast.RelationalOpExpr:
		return precCmp
	case *ast.StringConcatOpExpr:
		return precConcat
	case *ast.ArithmeticOpExpr:
		switch e.Operator {
		case "+", "-":
			return precAdd
		case "^":
			return precPow
		}
		return precMul
	case *ast.UnaryMinusOpExpr, *ast.UnaryNotOpExpr, *ast.UnaryLenOpExpr:
		return precUnary
	case *ast.NumberExpr:
		if p.raw(e, ast.TokenNumber) != "" {
			return precSimple
		}
		_, neg, nan := numeral(e.Value, p.jit)
		switch {
		case nan:
			return precMul
		case neg:
			return precUnary
		}
	case *ast.IdentExpr, *ast.AttrGetExpr, *ast.FuncCallExpr:
		return precPrefix
	}
	return precSimple
}

func adjustRet(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.FuncCallExpr:
		return e.AdjustRet
	case *ast.Comma3Expr:
		return e.AdjustRet
	}
	return false
}

// parenCount is the number of parentheses around e in a context of
// precedence min: those of the source, or one if needed, by precedence
// or, the last of a list (multi), for a call or "..." truncated to one
// value.
func (p *printer) parenCount(e ast.Expr, min int, multi bool) int {
	need := p.prec(e) < min || multi && adjustRet(e)
	n := 0
	if ps := e.Parens(); ps.Count > 0 && p.fromSource(e) && p.kindAt(ps.Span.Start) == ast.TokenLParen {
		n = ps.Count
	}
	if need && n == 0 {
		n = 1
	}
	return n
}

// expr prints an expression in a context of precedence min, with its
// comments.
func (p *printer) expr(e ast.Expr, min int, multi bool) doc {
	d, t := p.exprT(e, min, multi, false)
	return cat(d, t)
}

// exprT prints an expression and returns its trailing comments apart;
// bare prints it without parentheses (an assignment target, a call
// statement).
func (p *printer) exprT(e ast.Expr, min int, multi, bare bool) (doc, doc) {
	if isNil(e) {
		p.fail("missing expression")
	}
	p.enter()
	defer p.leave()
	lead := p.open(e)
	n := 0
	if !bare {
		n = p.parenCount(e, min, multi)
	}
	body := p.exprBody(e)
	if n > 0 {
		body = cat(text(strings.Repeat("(", n)), body, text(strings.Repeat(")", n)))
	}
	rest, trail := p.close()
	return cat(lead, body, rest), trail
}

func (p *printer) exprBody(e ast.Expr) doc {
	switch e := e.(type) {
	case *ast.NilExpr:
		return text("nil")
	case *ast.TrueExpr:
		return text("true")
	case *ast.FalseExpr:
		return text("false")
	case *ast.Comma3Expr:
		return text("...")
	case *ast.IdentExpr:
		p.checkName(e.Value)
		return text(e.Value)
	case *ast.NumberExpr:
		if r := p.raw(e, ast.TokenNumber); r != "" && p.sameNumber(r, e.Value) {
			return num(r)
		}
		s, neg, nan := numeral(e.Value, p.jit)
		switch {
		case nan:
			return text("0 / 0")
		case neg:
			return cat(text("-"), num(s))
		}
		return num(s)
	case *ast.CdataNumberExpr:
		if !p.jit {
			p.fail("LuaJIT cdata numeral %s under Lua 5.1", e.Value)
		}
		if !isCdataNumeral(e.Value) {
			p.fail("invalid cdata numeral %q", e.Value)
		}
		return num(e.Value)
	case *ast.StringExpr:
		return text(p.stringLit(e))
	case *ast.TableExpr:
		return p.table(e)
	case *ast.FunctionExpr:
		return cat(p.tok(ast.TokenFunction, "function"), p.funcBody(e))
	case *ast.AttrGetExpr:
		return p.index(e)
	case *ast.FuncCallExpr:
		return p.call(e)
	case *ast.LogicalOpExpr, *ast.RelationalOpExpr, *ast.StringConcatOpExpr, *ast.ArithmeticOpExpr:
		return p.binary(e)
	case *ast.UnaryMinusOpExpr:
		return cat(p.tok(ast.TokenMinus, "-"), p.expr(e.Expr, precUnary, false))
	case *ast.UnaryNotOpExpr:
		return cat(p.tok(ast.TokenNot, "not"), sp, p.expr(e.Expr, precUnary, false))
	case *ast.UnaryLenOpExpr:
		return cat(p.tok(ast.TokenHash, "#"), p.expr(e.Expr, precUnary, false))
	}
	p.fail("unknown expression %T", e)
	return nil
}

func (p *printer) checkName(s string) {
	if !isName(s, p.jit) {
		p.fail("%q is no name", s)
	}
}

func isCdataNumeral(s string) bool {
	toks, err := parse.Tokenize([]byte(s), "numeral", parse.Options{Dialect: parse.LuaJIT})
	return err == nil && len(toks) == 2 && toks[0].Kind == ast.TokenNumber && toks[0].Span().End == len(s)
}

// sameNumber reports whether the numeral raw has the value value, as the
// parser of the dialect gives it.
func (p *printer) sameNumber(raw, value string) bool {
	if !p.jit {
		// a NUL may end a Lua 5.1 numeral, which converts up to it
		if i := strings.IndexByte(raw, 0); i >= 0 {
			raw = raw[:i]
		}
		return raw == value
	}
	v, ok := parse.ScanNumberLuaJIT(raw)
	return ok && strconv.FormatFloat(v, 'g', -1, 64) == value
}

// stringLit returns the literal of a string: as written, its quotes
// normalized if asked, if it is the literal of the value; else quoted.
func (p *printer) stringLit(e *ast.StringExpr) string {
	if r := p.raw(e, ast.TokenString); r != "" && p.sameString(r, e.Value) {
		switch p.opts.Quotes {
		case QuotesDouble:
			return requote(r, false)
		case QuotesSingle:
			return requote(r, true)
		}
		return r
	}
	return quoteString(e.Value, p.opts.Quotes == QuotesSingle)
}

// sameString reports whether the literal raw reads as value.
func (p *printer) sameString(raw, value string) bool {
	if raw[0] == '[' {
		level := ast.LongBracketLevel([]byte(raw))
		t := raw[level+2 : len(raw)-level-2]
		if len(t) > 0 && (t[0] == '\n' || t[0] == '\r') {
			if len(t) > 1 && (t[1] == '\n' || t[1] == '\r') && t[1] != t[0] {
				t = t[2:]
			} else {
				t = t[1:]
			}
		}
		return normalizeNewlines(t) == value
	}
	body := raw[1 : len(raw)-1]
	if strings.IndexByte(body, '\\') < 0 {
		return body == value
	}
	return decodeString(raw, p.jit) == value
}

// decodeString returns the value of a short string literal with escapes,
// as the parser reads it, or "\xff\xfe" (no value the check compares with
// equal for such a literal... see sameString) if it does not parse.
func decodeString(raw string, jit bool) string {
	d := parse.Lua51
	if jit {
		d = parse.LuaJIT
	}
	stmts, err := parse.ParseBytes([]byte("return "+raw), "string", d)
	if err != nil || len(stmts) != 1 {
		return "\x00invalid"
	}
	r, ok := stmts[0].(*ast.ReturnStmt)
	if !ok || len(r.Exprs) != 1 {
		return "\x00invalid"
	}
	s, ok := r.Exprs[0].(*ast.StringExpr)
	if !ok {
		return "\x00invalid"
	}
	return s.Value
}

// keyIsName reports whether the key of an index or a field is printed as
// a name: as written in the source, else if it is a string that is a name.
func (p *printer) keyIsName(key ast.Expr) bool {
	s, ok := key.(*ast.StringExpr)
	if !ok || !isName(s.Value, p.jit) {
		return false
	}
	if p.fromSource(s) && s.Parens().Count == 0 {
		return p.raw(s, ast.TokenName) == s.Value
	}
	return true
}

// bracketKey prints "[key]", "[ [[key]] ]" for a key starting with a long
// bracket.
func (p *printer) bracketKey(key ast.Expr) doc {
	open := p.tok(ast.TokenLBracket, "[")
	k := p.expr(key, 0, false)
	if p.startsLong(key) {
		return cat(open, sp, k, sp, p.tok(ast.TokenRBracket, "]"))
	}
	return cat(open, k, p.tok(ast.TokenRBracket, "]"))
}

// startsLong reports whether e is printed starting with a long string.
func (p *printer) startsLong(e ast.Expr) bool {
	for !isNil(e) && p.parenCount(e, 0, false) == 0 {
		switch x := e.(type) {
		case *ast.StringExpr:
			return strings.HasPrefix(p.stringLit(x), "[")
		case *ast.LogicalOpExpr:
			e = x.Lhs
		case *ast.RelationalOpExpr:
			e = x.Lhs
		case *ast.StringConcatOpExpr:
			e = x.Lhs
		case *ast.ArithmeticOpExpr:
			e = x.Lhs
		case *ast.AttrGetExpr:
			e = x.Object
		case *ast.FuncCallExpr:
			if !isNil(x.Receiver) {
				e = x.Receiver
			} else {
				e = x.Func
			}
		default:
			return false
		}
	}
	return false
}

// nameKey prints a key written as a name, with its comments.
func (p *printer) nameKey(key ast.Expr) doc {
	lead := p.open(key)
	d := text(key.(*ast.StringExpr).Value)
	rest, trail := p.close()
	return cat(lead, d, rest, trail)
}

func (p *printer) index(e *ast.AttrGetExpr) doc {
	obj := p.expr(e.Object, precPrefix, false)
	if isNil(e.Key) {
		p.fail("index without a key")
	}
	if p.keyIsName(e.Key) {
		return cat(obj, p.tok(ast.TokenDot, "."), p.nameKey(e.Key))
	}
	return cat(obj, p.bracketKey(e.Key))
}

func (p *printer) call(e *ast.FuncCallExpr) doc {
	var fn, fnTrail doc
	if !isNil(e.Receiver) {
		p.checkName(e.Method)
		fn = cat(p.expr(e.Receiver, precPrefix, false), p.tok(ast.TokenColon, ":"), p.tok(ast.TokenName, e.Method))
	} else {
		if isNil(e.Func) {
			p.fail("call without a function")
		}
		fn, fnTrail = p.exprT(e.Func, precPrefix, false, false)
	}
	return cat(fn, p.args(e, fnTrail))
}

// sugar reports whether a call is written f"s" or f{...} in the source.
func (p *printer) sugar(e *ast.FuncCallExpr) bool {
	if len(e.Args) != 1 || !p.fromSource(e) {
		return false
	}
	a := e.Args[0]
	switch a.(type) {
	case *ast.StringExpr, *ast.TableExpr:
	default:
		return false
	}
	if !p.fromSource(a) || a.Parens().Count > 0 || a.Span().End != e.Span().End {
		return false
	}
	k := p.kindAt(a.Span().Start)
	if k != ast.TokenString && k != ast.TokenLBrace {
		return false
	}
	return p.prevKind(a.Span().Start) != ast.TokenLParen
}

func (p *printer) args(e *ast.FuncCallExpr, fnTrail doc) doc {
	if p.sugar(e) {
		a := e.Args[0]
		d := p.expr(a, 0, false)
		if _, ok := a.(*ast.StringExpr); ok {
			return cat(fnTrail, sp, d)
		}
		return cat(fnTrail, d)
	}
	// comments before the "(" go after it: a line break before it would
	// make a new statement of the arguments
	open := cat(text("("), p.before(ast.TokenLParen), fnTrail)
	if len(e.Args) == 0 {
		ds := p.dangling()
		closeTok := p.tok(ast.TokenRParen, ")")
		if len(ds) == 0 {
			return cat(open, closeTok)
		}
		return dangle(open, ds, closeTok)
	}
	pre := p.sameLineLead(e.Args[0])
	listComments := pre != nil
	last := len(e.Args) - 1
	items := make([]doc, 0, 4*len(e.Args))
	docs := make([]doc, len(e.Args))
	hard := 0 // the arguments holding a line break
	for i, a := range e.Args {
		if i > 0 {
			items = append(items, lineSpace)
		}
		d, t := p.exprT(a, 0, i == last, false)
		docs[i] = d
		if isHard(d) {
			hard++
		}
		items = append(items, d)
		if i < last {
			c := p.before(ast.TokenComma)
			items = append(items, c, text(","))
			listComments = listComments || c != nil
		}
		items = append(items, t)
		listComments = listComments || t != nil || p.hasLeading(a)
	}
	c := p.before(ast.TokenRParen)
	listComments = listComments || c != nil
	closeTok := cat(c, text(")"))
	expanded := mkGroup(cat(open, pre, mkNest(cat(lineSoft, catList(items))), lineSoft, closeTok))
	if listComments {
		return expanded // comments between the arguments: one a line
	}
	// the last argument opens on the line of the call, f(a, function()
	// ... end), or the first, f(function() ... end, a), the others flat
	ch := &choice{expanded: expanded}
	if p.huggable(e.Args[last]) && (hard == 0 || hard == 1 && isHard(docs[last])) {
		ch.hugs = append(ch.hugs, hugArgs(open, docs, last, closeTok))
	}
	if last > 0 && p.huggable(e.Args[0]) && isFunction(e.Args[0]) && (hard == 0 || hard == 1 && isHard(docs[0])) {
		ch.hugs = append(ch.hugs, hugArgs(open, docs, 0, closeTok))
	}
	if len(ch.hugs) == 0 {
		return expanded
	}
	return ch
}

// hugArgs lays out arguments with the one hugged broken, the others flat.
func hugArgs(open doc, docs []doc, hugged int, closeTok doc) doc {
	hug := make([]doc, 0, 3*len(docs)+2)
	hug = append(hug, open)
	for i, d := range docs {
		if i > 0 {
			hug = append(hug, text(","), sp)
		}
		if i == hugged {
			hug = append(hug, forceBreak(d))
		} else {
			hug = append(hug, &flat{d})
		}
	}
	hug = append(hug, closeTok)
	return catList(hug)
}

func isFunction(e ast.Expr) bool {
	_, ok := e.(*ast.FunctionExpr)
	return ok
}

// huggable reports whether the last argument of a call may open on the
// line of the call and close on the line of its ")": a table or a
// function without parentheses.
func (p *printer) huggable(a ast.Expr) bool {
	switch a.(type) {
	case *ast.TableExpr, *ast.FunctionExpr:
		return p.parenCount(a, 0, true) == 0
	}
	return false
}

// binary prints a chain of binary operators of one level.
func (p *printer) binary(e ast.Expr) doc {
	var parts []doc
	p.chain(e, p.prec(e), &parts)
	k := 0
	for k < len(parts) && parts[k] != lineSpace {
		k++
	}
	return mkGroup(cat(catList(parts[:k]), mkNest(catList(parts[k:]))))
}

func binaryParts(e ast.Expr) (op string, lhs, rhs ast.Expr, kind ast.TokenKind) {
	switch e := e.(type) {
	case *ast.LogicalOpExpr:
		op, lhs, rhs = e.Operator, e.Lhs, e.Rhs
	case *ast.RelationalOpExpr:
		op, lhs, rhs = e.Operator, e.Lhs, e.Rhs
	case *ast.StringConcatOpExpr:
		op, lhs, rhs = "..", e.Lhs, e.Rhs
	case *ast.ArithmeticOpExpr:
		op, lhs, rhs = e.Operator, e.Lhs, e.Rhs
	}
	return op, lhs, rhs, opKinds[op]
}

var opKinds = map[string]ast.TokenKind{
	"or": ast.TokenOr, "and": ast.TokenAnd, "==": ast.TokenEq, "~=": ast.TokenNe,
	"<": ast.TokenLt, "<=": ast.TokenLe, ">": ast.TokenGt, ">=": ast.TokenGe,
	"..": ast.TokenConcat, "+": ast.TokenPlus, "-": ast.TokenMinus, "*": ast.TokenStar,
	"/": ast.TokenSlash, "%": ast.TokenPercent, "^": ast.TokenCaret,
}

func (p *printer) chainable(e ast.Expr, level int) bool {
	switch e.(type) {
	case *ast.LogicalOpExpr, *ast.RelationalOpExpr, *ast.StringConcatOpExpr, *ast.ArithmeticOpExpr:
		return p.prec(e) == level && p.parenCount(e, level, false) == 0
	}
	return false
}

// chain appends the parts of the binary operation e of the level, and of
// the operations of the level that are its operands on the side of its
// associativity: the operands, each operator followed by a line.
func (p *printer) chain(e ast.Expr, level int, parts *[]doc) {
	op, lhs, rhs, kind := binaryParts(e)
	if kind == ast.TokenInvalid {
		p.fail("unknown operator %q", op)
	}
	if isNil(lhs) || isNil(rhs) {
		p.fail("missing operand of %q", op)
	}
	switch e := e.(type) {
	case *ast.LogicalOpExpr:
		if op != "and" && op != "or" {
			p.fail("unknown logical operator %q", op)
		}
	case *ast.RelationalOpExpr:
		if p.prec(e) != precCmp || kind < ast.TokenEq || kind > ast.TokenGt {
			p.fail("unknown relational operator %q", op)
		}
	case *ast.ArithmeticOpExpr:
		if kind < ast.TokenPlus || kind > ast.TokenCaret {
			p.fail("unknown arithmetic operator %q", op)
		}
	}
	right := level == precConcat || level == precPow
	lmin, rmin := level, level+1
	if right {
		lmin, rmin = level+1, level
	}
	if level == precPow {
		lmin, rmin = precSimple, precUnary
	}
	sub := func(x ast.Expr, min int, chain bool) {
		if chain && p.chainable(x, level) {
			p.enter()
			*parts = append(*parts, p.open(x))
			p.chain(x, level, parts)
			rest, trail := p.close()
			*parts = append(*parts, rest, trail)
			p.leave()
			return
		}
		*parts = append(*parts, p.expr(x, min, false))
	}
	sub(lhs, lmin, !right)
	*parts = append(*parts, sp, p.tok(kind, op), lineSpace)
	sub(rhs, rmin, right)
}

/* tables and functions */

func (p *printer) table(t *ast.TableExpr) doc {
	open := p.tok(ast.TokenLBrace, "{")
	if len(t.Fields) == 0 {
		ds := p.dangling()
		closeTok := p.tok(ast.TokenRBrace, "}")
		if len(ds) == 0 {
			return cat(open, closeTok)
		}
		return dangle(open, ds, closeTok)
	}
	broken := p.fromSource(t) && p.newlineAfter(t.Span().Start)
	pre := p.sameLineLead(t.Fields[0])
	last := len(t.Fields) - 1
	items := make([]doc, 0, 4*len(t.Fields))
	for i, f := range t.Fields {
		if f == nil {
			p.fail("nil table field")
		}
		if i > 0 {
			items = append(items, lineSpace)
		}
		d, tr, bl := p.field(f, i == last && f.Key == nil, i == 0)
		broken = broken || bl
		items = append(items, d)
		if i < last {
			items = append(items, p.tok(ast.TokenComma, ","))
		} else {
			items = append(items, p.before(ast.TokenComma, ast.TokenSemicolon))
			if p.opts.TrailingComma || p.trailingSep(t) {
				items = append(items, &ifBreak{broken: text(",")})
			}
		}
		items = append(items, tr)
	}
	closeTok := p.tok(ast.TokenRBrace, "}")
	d := cat(open, pre, mkNest(cat(lineSoft, catList(items))), lineSoft, closeTok)
	return &group{d: d, hard: broken || isHard(d)}
}

// trailingSep reports whether the table has a separator after its last
// field in the source.
func (p *printer) trailingSep(t *ast.TableExpr) bool {
	if !p.fromSource(t) {
		return false
	}
	f := t.Fields[len(t.Fields)-1]
	if !p.fromSource(f) {
		return false
	}
	k := p.nextKind(f.Span().End)
	return k == ast.TokenComma || k == ast.TokenSemicolon
}

func (p *printer) field(f *ast.Field, multi, first bool) (doc, doc, bool) {
	p.enter()
	defer p.leave()
	p.noBlank = first
	lead := p.open(f)
	p.noBlank = false
	var bl doc
	if (!first || lead != nil) && p.blankBefore(f) {
		bl = blank{}
	}
	if isNil(f.Value) {
		p.fail("table field without a value")
	}
	var body doc
	switch {
	case isNil(f.Key):
		body = p.expr(f.Value, 0, multi)
	case p.keyIsName(f.Key):
		body = cat(p.nameKey(f.Key), sp, p.tok(ast.TokenAssign, "="), sp, p.expr(f.Value, 0, false))
	default:
		body = cat(p.bracketKey(f.Key), sp, p.tok(ast.TokenAssign, "="), sp, p.expr(f.Value, 0, false))
	}
	rest, trail := p.close()
	return cat(lead, bl, body, rest), trail, bl != nil
}

// funcBody prints the parameters, the body and the "end" of a function.
func (p *printer) funcBody(fn *ast.FunctionExpr) doc {
	params := p.parList(fn.ParList)
	one := p.oneLine(fn, fn.Stmts)
	return p.maybeGroup(one, cat(params, p.block(fn.Stmts, one), p.tok(ast.TokenEnd, "end")))
}

// funcElem prints a function that is a child element (of a function
// statement or a local function): its body, with its comments.
func (p *printer) funcElem(fn *ast.FunctionExpr) doc {
	if fn == nil {
		p.fail("function statement without a function")
	}
	lead := p.open(fn)
	body := p.funcBody(fn)
	rest, trail := p.close()
	return cat(lead, body, rest, trail)
}

func (p *printer) parList(pl *ast.ParList) doc {
	if pl == nil {
		return text("()")
	}
	lead := p.open(pl)
	open := p.tok(ast.TokenLParen, "(")
	var items []doc
	for i, name := range pl.Names {
		p.checkName(name)
		if i > 0 {
			items = append(items, p.tok(ast.TokenComma, ","), lineSpace)
		}
		items = append(items, p.tok(ast.TokenName, name))
	}
	if pl.HasVargs {
		if len(pl.Names) > 0 {
			items = append(items, p.tok(ast.TokenComma, ","), lineSpace)
		}
		items = append(items, p.tok(ast.TokenDots, "..."))
	}
	var d doc
	if len(items) == 0 {
		ds := p.dangling()
		closeTok := p.tok(ast.TokenRParen, ")")
		if len(ds) == 0 {
			d = cat(open, closeTok)
		} else {
			d = dangle(open, ds, closeTok)
		}
	} else {
		ds := p.dangling()
		closeTok := p.tok(ast.TokenRParen, ")")
		d = mkGroup(cat(open, mkNest(cat(lineSoft, catList(items), catList(ds))), lineSoft, closeTok))
	}
	rest, trail := p.close()
	return cat(lead, d, rest, trail)
}

/* statements */

// block prints the statements of a block that the element being printed
// holds, and the comments at its end; oneLine lays it out on the line of
// its keywords if it fits (the caller groups them).
func (p *printer) block(list []ast.Stmt, oneLine bool) doc {
	var pre doc
	if len(list) > 0 {
		pre = p.sameLineLead(list[0])
	}
	parts := p.stmtList(list)
	ds := p.dangling()
	if len(list) == 0 {
		pre, ds = splitSameLine(noBlankFirst(ds))
	}
	parts = append(parts, ds...)
	body := catList(parts)
	if body == nil {
		if pre != nil {
			return cat(pre, lineHard)
		}
		return sp
	}
	sep := lineHard
	if oneLine {
		sep = lineSpace
	}
	return cat(pre, mkNest(cat(sep, body)), sep)
}

// stmtList prints a list of statements, one a line.
func (p *printer) stmtList(list []ast.Stmt) []doc {
	parts := make([]doc, 0, 4*len(list))
	for i, s := range list {
		if isNil(s) {
			p.fail("nil statement")
		}
		if i > 0 {
			parts = append(parts, lineHard)
		}
		p.noBlank = i == 0
		lead := p.open(s)
		p.noBlank = false
		if (i > 0 || lead != nil) && p.blankBefore(s) {
			lead = cat(lead, blank{})
		}
		body := p.stmt(s)
		rest, trail := p.close()
		parts = append(parts, lead, body, rest)
		if i+1 < len(list) && p.startsWithParen(list[i+1]) || p.opts.KeepSemicolons && p.hasSemicolon(s) {
			if c := p.before(ast.TokenSemicolon); c != nil {
				parts = append(parts, trail, c, text(";"))
			} else {
				parts = append(parts, text(";"), trail)
			}
		} else {
			parts = append(parts, trail)
		}
	}
	return parts
}

// hasSemicolon reports whether a ";" follows s in the source.
func (p *printer) hasSemicolon(s ast.Stmt) bool {
	return p.fromSource(s) && p.nextKind(s.Span().End) == ast.TokenSemicolon
}

// startsWithParen reports whether a statement is printed starting with
// "(": after another statement, it needs a ";" not to be read as the
// arguments of a call ending that statement.
func (p *printer) startsWithParen(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.AssignStmt:
		if len(s.Lhs) > 0 && !isNil(s.Lhs[0]) {
			return p.leftParen(s.Lhs[0], true)
		}
	case *ast.FuncCallStmt:
		if !isNil(s.Expr) {
			return p.leftParen(s.Expr, true)
		}
	}
	return false
}

func (p *printer) leftParen(e ast.Expr, bare bool) bool {
	if !bare && p.parenCount(e, precPrefix, false) > 0 {
		return true
	}
	switch e := e.(type) {
	case *ast.AttrGetExpr:
		return !isNil(e.Object) && p.leftParen(e.Object, false)
	case *ast.FuncCallExpr:
		if !isNil(e.Receiver) {
			return p.leftParen(e.Receiver, false)
		}
		return !isNil(e.Func) && p.leftParen(e.Func, false)
	}
	return false
}

// exprList prints a list of expressions; its last one is multi.
func (p *printer) exprList(list []ast.Expr, multi bool) doc {
	if len(list) == 1 {
		return p.expr(list[0], 0, multi)
	}
	items := make([]doc, 0, 3*len(list))
	for i, e := range list {
		if i > 0 {
			items = append(items, lineSpace)
		}
		d, t := p.exprT(e, 0, multi && i == len(list)-1, false)
		items = append(items, d)
		if i < len(list)-1 {
			items = append(items, p.tok(ast.TokenComma, ","))
		}
		items = append(items, t)
	}
	return mkGroup(mkNest(catList(items)))
}

// names prints the names of a local statement or a for loop.
func (p *printer) names(list []string) doc {
	if len(list) == 0 {
		p.fail("no names")
	}
	items := make([]doc, 0, 3*len(list))
	for i, n := range list {
		p.checkName(n)
		if i > 0 {
			items = append(items, p.tok(ast.TokenComma, ","), sp)
		}
		items = append(items, p.tok(ast.TokenName, n))
	}
	return catList(items)
}

func (p *printer) target(e ast.Expr) (doc, doc) {
	switch e.(type) {
	case *ast.IdentExpr, *ast.AttrGetExpr:
	default:
		p.fail("cannot assign to %T", e)
	}
	return p.exprT(e, 0, false, true)
}

func (p *printer) stmt(s ast.Stmt) doc {
	p.enter()
	defer p.leave()
	switch s := s.(type) {
	case *ast.AssignStmt:
		if len(s.Lhs) == 0 || len(s.Rhs) == 0 {
			p.fail("assignment without targets or values")
		}
		var lhs []doc
		for i, e := range s.Lhs {
			if isNil(e) {
				p.fail("missing assignment target")
			}
			if i > 0 {
				lhs = append(lhs, lineSpace)
			}
			d, t := p.target(e)
			lhs = append(lhs, d)
			if i < len(s.Lhs)-1 {
				lhs = append(lhs, p.tok(ast.TokenComma, ","))
			}
			lhs = append(lhs, t)
		}
		l := catList(lhs)
		if len(s.Lhs) > 1 {
			l = mkGroup(mkNest(l))
		}
		return cat(l, sp, p.tok(ast.TokenAssign, "="), sp, p.exprList(s.Rhs, true))
	case *ast.LocalAssignStmt:
		if s.IsFunction {
			if len(s.Names) != 1 || len(s.Exprs) != 1 {
				p.fail("local function with %d names and %d values", len(s.Names), len(s.Exprs))
			}
			fn, ok := s.Exprs[0].(*ast.FunctionExpr)
			if !ok {
				p.fail("local function of a %T", s.Exprs[0])
			}
			p.checkName(s.Names[0])
			return cat(p.tok(ast.TokenLocal, "local"), sp, p.tok(ast.TokenFunction, "function"), sp,
				p.tok(ast.TokenName, s.Names[0]), p.funcElem(fn))
		}
		d := cat(p.tok(ast.TokenLocal, "local"), sp, p.names(s.Names))
		if len(s.Exprs) > 0 {
			d = cat(d, sp, p.tok(ast.TokenAssign, "="), sp, p.exprList(s.Exprs, true))
		}
		return d
	case *ast.FuncCallStmt:
		if _, ok := s.Expr.(*ast.FuncCallExpr); !ok {
			p.fail("call statement of a %T", s.Expr)
		}
		d, t := p.exprT(s.Expr, 0, false, true)
		return cat(d, t)
	case *ast.DoBlockStmt:
		one := p.oneLine(s, s.Stmts)
		return p.maybeGroup(one, cat(p.tok(ast.TokenDo, "do"), p.block(s.Stmts, one), p.tok(ast.TokenEnd, "end")))
	case *ast.WhileStmt:
		one := p.oneLine(s, s.Stmts)
		return p.maybeGroup(one, cat(p.tok(ast.TokenWhile, "while"), sp, p.cond(s.Condition), sp,
			p.tok(ast.TokenDo, "do"), p.block(s.Stmts, one), p.tok(ast.TokenEnd, "end")))
	case *ast.RepeatStmt:
		one := p.oneLine(s, s.Stmts)
		return p.maybeGroup(one, cat(p.tok(ast.TokenRepeat, "repeat"), p.block(s.Stmts, one),
			p.tok(ast.TokenUntil, "until"), sp, p.expr(s.Condition, 0, false)))
	case *ast.IfStmt:
		return p.ifStmt(s)
	case *ast.NumberForStmt:
		p.checkName(s.Name)
		one := p.oneLine(s, s.Stmts)
		d := cat(p.tok(ast.TokenFor, "for"), sp, p.tok(ast.TokenName, s.Name), sp, p.tok(ast.TokenAssign, "="), sp,
			p.expr(s.Init, 0, false), p.tok(ast.TokenComma, ","), sp, p.expr(s.Limit, 0, false))
		if !isNil(s.Step) {
			d = cat(d, p.tok(ast.TokenComma, ","), sp, p.expr(s.Step, 0, false))
		}
		return p.maybeGroup(one, cat(d, sp, p.tok(ast.TokenDo, "do"), p.block(s.Stmts, one), p.tok(ast.TokenEnd, "end")))
	case *ast.GenericForStmt:
		if len(s.Exprs) == 0 {
			p.fail("for loop without values")
		}
		one := p.oneLine(s, s.Stmts)
		return p.maybeGroup(one, cat(p.tok(ast.TokenFor, "for"), sp, p.names(s.Names), sp, p.tok(ast.TokenIn, "in"), sp,
			p.exprList(s.Exprs, true), sp, p.tok(ast.TokenDo, "do"), p.block(s.Stmts, one), p.tok(ast.TokenEnd, "end")))
	case *ast.FuncDefStmt:
		if s.Name == nil {
			p.fail("function statement without a name")
		}
		return cat(p.tok(ast.TokenFunction, "function"), sp, p.funcName(s.Name), p.funcElem(s.Func))
	case *ast.ReturnStmt:
		if len(s.Exprs) == 0 {
			return p.tok(ast.TokenReturn, "return")
		}
		return cat(p.tok(ast.TokenReturn, "return"), sp, p.exprList(s.Exprs, true))
	case *ast.BreakStmt:
		return p.tok(ast.TokenBreak, "break")
	case *ast.LabelStmt:
		if !p.jit {
			p.fail("label under Lua 5.1")
		}
		p.checkName(s.Name)
		return cat(p.tok(ast.TokenDoubleColon, "::"), p.tok(ast.TokenName, s.Name), p.tok(ast.TokenDoubleColon, "::"))
	case *ast.GotoStmt:
		if !p.jit {
			p.fail("goto under Lua 5.1")
		}
		p.checkName(s.Label)
		return cat(p.tok(ast.TokenGoto, "goto"), sp, p.tok(ast.TokenName, s.Label))
	}
	p.fail("unknown statement %T", s)
	return nil
}

func (p *printer) maybeGroup(one bool, d doc) doc {
	if one {
		return &group{d: d, hard: isHard(d), strict: true}
	}
	return d
}

func (p *printer) funcName(n *ast.FuncName) doc {
	lead := p.open(n)
	var d doc
	if !isNil(n.Receiver) {
		p.checkName(n.Method)
		d = cat(p.dotted(n.Receiver), p.tok(ast.TokenColon, ":"), p.tok(ast.TokenName, n.Method))
	} else {
		if isNil(n.Func) {
			p.fail("function name without a name")
		}
		d = p.dotted(n.Func)
	}
	rest, trail := p.close()
	return cat(lead, d, rest, trail)
}

// dotted prints a name of a function statement: a name and fields.
func (p *printer) dotted(e ast.Expr) doc {
	p.enter()
	defer p.leave()
	lead := p.open(e)
	var d doc
	switch e := e.(type) {
	case *ast.IdentExpr:
		p.checkName(e.Value)
		d = text(e.Value)
	case *ast.AttrGetExpr:
		k, ok := e.Key.(*ast.StringExpr)
		if !ok || !isName(k.Value, p.jit) || isNil(e.Object) {
			p.fail("function name with a key that is no name")
		}
		d = cat(p.dotted(e.Object), p.tok(ast.TokenDot, "."), p.nameKey(k))
	default:
		p.fail("function name of a %T", e)
	}
	rest, trail := p.close()
	return cat(lead, d, rest, trail)
}

func (p *printer) ifStmt(s *ast.IfStmt) doc {
	one := p.ifOneLine(s)
	multi := len(s.Else) > 0 || p.fromSource(s) && s.ElseSpan != (ast.Span{})
	cur := s
	var opened []*ast.IfStmt
	out := []doc{p.tok(ast.TokenIf, "if"), sp, p.cond(s.Condition), sp, p.tok(ast.TokenThen, "then")}
	bp := p.blockParts(cur.Then, multi)
	for {
		if next := p.elseif(cur); next != nil {
			// the comments above the "elseif" go at the end of the block
			bp.tail = append(bp.tail, p.open(next))
			out = append(out, p.wrap(bp, one),
				p.tok(ast.TokenElseif, "elseif"), sp, p.cond(next.Condition), sp, p.tok(ast.TokenThen, "then"))
			bp = p.blockParts(next.Then, multi)
			opened = append(opened, next)
			cur = next
			continue
		}
		if len(cur.Else) > 0 || p.fromSource(cur) && cur.ElseSpan != (ast.Span{}) && p.elseif(cur) == nil {
			bp.tail = append(bp.tail, p.beforeDocs(ast.TokenElse)...)
			out = append(out, p.wrap(bp, one), p.tok(ast.TokenElse, "else"))
			bp = p.blockParts(cur.Else, multi)
		}
		break
	}
	for range opened {
		bp.tail = append(bp.tail, p.dangling()...)
		rest, trail := p.close()
		bp.tail = append(bp.tail, rest, trail)
	}
	bp.tail = append(bp.tail, p.dangling()...)
	out = append(out, p.wrap(bp, one), p.tok(ast.TokenEnd, "end"))
	return p.maybeGroup(one, catList(out))
}

// ifOneLine reports whether an if statement is on one line of the source
// with at most a statement in each of its blocks.
func (p *printer) ifOneLine(s *ast.IfStmt) bool {
	if !p.oneLine(s) {
		return false
	}
	for cur := s; ; {
		if len(cur.Then) > 1 {
			return false
		}
		next := p.elseif(cur)
		if next == nil {
			return len(cur.Else) <= 1
		}
		cur = next
	}
}

// elseif returns the IfStmt that is the "elseif" of s, or nil.
func (p *printer) elseif(s *ast.IfStmt) *ast.IfStmt {
	if len(s.Else) != 1 {
		return nil
	}
	next, ok := s.Else[0].(*ast.IfStmt)
	if !ok || next == nil {
		return nil
	}
	if p.fromSource(next) && p.kindAt(next.Span().Start) == ast.TokenIf {
		return nil // "else if ... end" as written
	}
	return next
}

// blockParts are the parts of a block whose end is not known yet.
type blockParts struct {
	empty bool
	multi bool // one of several blocks
	pre   doc
	parts []doc
	tail  []doc
}

func (p *printer) blockParts(list []ast.Stmt, multi bool) *blockParts {
	bp := &blockParts{empty: len(list) == 0, multi: multi}
	if len(list) > 0 {
		bp.pre = p.sameLineLead(list[0])
	}
	bp.parts = p.stmtList(list)
	return bp
}

func (p *printer) wrap(bp *blockParts, oneLine bool) doc {
	pre := bp.pre
	tail := bp.tail
	if bp.empty {
		var t []doc
		for _, d := range tail {
			if d != nil {
				t = append(t, d)
			}
		}
		pre, tail = splitSameLine(noBlankFirst(t))
	}
	body := cat(catList(bp.parts), catList(tail))
	if body == nil {
		switch {
		case pre != nil:
			return cat(pre, lineHard)
		case !oneLine && bp.multi:
			return lineHard // "else" on a line of its own
		}
		return sp
	}
	sep := lineHard
	if oneLine {
		sep = lineSpace
	}
	return cat(pre, mkNest(cat(sep, body)), sep)
}

// cond prints the condition of an if or a while statement: its lines
// indented two levels, not to align with the block.
func (p *printer) cond(e ast.Expr) doc {
	return mkNest(p.expr(e, 0, false))
}

// space is a space between tokens, never written at the end of a line.
type space struct{}

var sp doc = space{}
