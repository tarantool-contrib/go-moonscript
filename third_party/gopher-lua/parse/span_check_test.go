package parse

// go-moonscript patch: a checker of the spans, comments and lines of a
// parsed chunk, for the span tests and the fuzz test.

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
)

// spanChecker checks a chunk parsed from src against the tokens the lexer
// reads from src:
//
//   - every element has a span from a token's start to a token's end, in
//     its parent's span (with the parentheses), after its previous sibling;
//   - its parentheses are Count "(" tokens before it and Count ")" after;
//   - a leaf's text is its token (a name, a string or number whose value is
//     the node's, a keyword), a name's span is the name, a block's span
//     is its statements and separators, a statement starts with its
//     keyword;
//   - the line of each token's offset (ast.Lines) is the line the lexer
//     gives it, and a leaf's Line is its token's;
//   - with reparse, the text of each element parses, in a context that
//     accepts it, to the same tree;
//   - the comments, the header, the tokens and white space make up the
//     whole source, and each comment's text is what the lexer read.
type spanChecker struct {
	tb      testing.TB
	name    string
	src     []byte
	opts    Options
	chunk   *ast.Chunk
	tokens  []token
	byStart map[int]int // token start -> index
	byEnd   map[int]int // token end -> index
	reparse bool
	errors  int
	checked int // elements checked
	parsed  int // elements reparsed
}

const maxSpanErrors = 20

func dialectName(d Dialect) string {
	if d == LuaJIT {
		return "luajit"
	}
	return "lua51"
}

func (c *spanChecker) errorf(format string, args ...any) {
	c.errors++
	if c.errors <= maxSpanErrors {
		c.tb.Helper()
		c.tb.Errorf("%s (%s): %s", c.name, dialectName(c.opts.Dialect), fmt.Sprintf(format, args...))
	}
}

// checkSpans checks the chunk parsed from src with opts (see spanChecker)
// and returns the number of elements checked and reparsed.
func checkSpans(tb testing.TB, name string, src []byte, opts Options, chunk *ast.Chunk, reparse bool) (int, int) {
	tb.Helper()
	c := &spanChecker{tb: tb, name: name, src: src, opts: opts, chunk: chunk, reparse: reparse,
		byStart: map[int]int{}, byEnd: map[int]int{}}
	c.lex()
	c.checkLines()
	c.checkCover()
	if chunk.Span() != (ast.Span{Start: 0, End: len(src)}) {
		c.errorf("chunk span %v", chunk.Span())
	}
	c.checkBlock(chunk.StmtsSpan, chunk.Stmts, "chunk")
	v := &spanVisitor{c: c}
	ast.Walk(v, chunk)
	return c.checked, c.parsed
}

// lex reads the tokens of the source.
func (c *spanChecker) lex() {
	defer func() {
		if e := recover(); e != nil {
			c.errorf("the lexer fails: %v", e)
		}
	}()
	ls := newLexer(c.src, c.name, c.opts.Dialect, c.opts.Shebang)
	for {
		ls.nextToken()
		if ls.t.typ == tkEOS {
			return
		}
		c.byStart[ls.t.start] = len(c.tokens)
		c.byEnd[ls.t.end] = len(c.tokens)
		c.tokens = append(c.tokens, ls.t)
	}
}

// checkLines checks the lines of the tokens' offsets.
func (c *spanChecker) checkLines() {
	lines := c.chunk.Lines
	for _, t := range c.tokens {
		if p := lines.Pos(t.start); p.Line != t.line || p.Offset != t.start ||
			p.Column != t.start-lines.Start(p.Line)+1 {
			c.errorf("token %q at %d: line %d, Lines.Pos %v", c.src[t.start:t.end], t.start, t.line, p)
		}
	}
}

func isWhite(b byte) bool { return b == ' ' || '\t' <= b && b <= '\r' }

// checkCover checks that the header, the comments, the tokens and white
// space make up the source, and the comments.
func (c *spanChecker) checkCover() {
	type piece struct {
		span ast.Span
		what string
	}
	var pieces []piece
	if h := c.chunk.Header; h != (ast.Span{}) {
		pieces = append(pieces, piece{h, "header"})
		if h.Start != 0 {
			c.errorf("header %v", h)
		}
		b := c.src[h.Start:h.End]
		if bytes.HasPrefix(b, []byte("\xef\xbb\xbf")) && c.opts.Dialect == LuaJIT {
			b = b[3:]
		}
		if len(b) > 0 && (b[0] != '#' || bytes.ContainsAny(b, "\r\n")) {
			c.errorf("header %q", b)
		}
	} else if c.opts.Dialect == LuaJIT && (bytes.HasPrefix(c.src, []byte("\xef\xbb\xbf")) || bytes.HasPrefix(c.src, []byte("#"))) ||
		c.opts.Shebang && bytes.HasPrefix(c.src, []byte("#")) {
		c.errorf("no header")
	}
	if c.opts.Comments {
		for _, cm := range c.chunk.Comments {
			pieces = append(pieces, piece{cm.Span, "comment"})
			c.checkComment(cm)
		}
	} else if c.chunk.Comments != nil {
		c.errorf("comments not asked for")
	}
	tokens := c.tokens
	at := 0
	for _, p := range pieces {
		for len(tokens) > 0 && tokens[0].start < p.span.Start {
			c.checkGap(at, tokens[0].start)
			at = tokens[0].end
			tokens = tokens[1:]
		}
		c.checkGap(at, p.span.Start)
		at = p.span.End
	}
	for _, t := range tokens {
		c.checkGap(at, t.start)
		at = t.end
	}
	if c.opts.Comments {
		c.checkGap(at, len(c.src))
	}
}

// checkGap checks that there is only white space from i to j.
func (c *spanChecker) checkGap(i, j int) {
	if i > j {
		c.errorf("pieces overlap at %d-%d", j, i)
		return
	}
	if !c.opts.Comments {
		return // comments in the gaps
	}
	for k := i; k < j; k++ {
		if !isWhite(c.src[k]) {
			c.errorf("%q at %d is no token, comment or space", c.src[k:min(j, k+20)], k)
			return
		}
	}
}

func (c *spanChecker) checkComment(cm ast.Comment) {
	s := cm.Span
	if s.Start < 0 || s.End > len(c.src) || s.End < s.Start+2 || string(c.src[s.Start:s.Start+2]) != "--" {
		c.errorf("comment %v", s)
		return
	}
	body := c.src[s.Start+2 : s.End]
	if !cm.Long {
		if string(body) != cm.Text || bytes.ContainsAny(body, "\r\n") {
			c.errorf("short comment %q: %q", body, cm.Text)
		}
		if s.End < len(c.src) && !(c.src[s.End] == '\n' || c.src[s.End] == '\r') {
			c.errorf("short comment %q ends before %q", body, c.src[s.End])
		}
		return
	}
	// a long comment is a long bracket, which lexes as a string
	ls := newLexer(body, "comment", c.opts.Dialect, false)
	func() {
		defer func() {
			if e := recover(); e != nil {
				c.errorf("long comment %q: %v", body, e)
			}
		}()
		ls.nextToken()
		if ls.t.typ != tkString || ls.t.end != len(body) || ls.t.sem != cm.Text {
			c.errorf("long comment %q: %q", body, cm.Text)
		}
	}()
}

// tokenText is the text from the token starting at start to the one ending
// at end, checking they are tokens.
func (c *spanChecker) checkSpan(s ast.Span, what string) bool {
	if s.Start < 0 || s.End > len(c.src) || s.Start >= s.End {
		c.errorf("%s: span %v", what, s)
		return false
	}
	_, ok1 := c.byStart[s.Start]
	_, ok2 := c.byEnd[s.End]
	if !ok1 || !ok2 {
		c.errorf("%s: span %v %q is not from a token to a token", what, s, c.src[s.Start:s.End])
		return false
	}
	return true
}

// checkBlock checks the span of a block.
func (c *spanChecker) checkBlock(s ast.Span, stmts []ast.Stmt, what string) {
	if len(stmts) == 0 {
		if s.Start != s.End || s.Start < 0 || s.Start > len(c.src) {
			c.errorf("%s: empty block %v", what, s)
			return
		}
		if _, ok := c.byStart[s.Start]; !ok && s.Start != c.eof() {
			c.errorf("%s: empty block %v not at a token", what, s)
		}
		return
	}
	if !c.checkSpan(s, what+" block") {
		return
	}
	if first := stmts[0].Span(); first.Start != s.Start {
		c.errorf("%s: block %v starts before its first statement %v", what, s, first)
	}
	last := stmts[len(stmts)-1].Span()
	if last.End != s.End {
		i := c.byEnd[s.End]
		if c.tokens[i].typ != ';' || c.tokens[i-1].end != last.End {
			c.errorf("%s: block %v ends after its last statement %v", what, s, last)
		}
	}
}

// eof is the offset of the end of the source as the lexer sees it.
func (c *spanChecker) eof() int { return len(c.src) }

func (c *spanChecker) text(s ast.Span) string { return string(c.src[s.Start:s.End]) }

// checkName checks the span of a name.
func (c *spanChecker) checkName(s ast.Span, name, what string) {
	if !c.checkSpan(s, what) {
		return
	}
	if t := c.text(s); t != name {
		c.errorf("%s %q spans %q", what, name, t)
	}
}

func (c *spanChecker) checkNames(spans []ast.Span, names []string, what string) {
	if len(spans) != len(names) {
		c.errorf("%s: %d spans for %d names", what, len(spans), len(names))
		return
	}
	for i, s := range spans {
		c.checkName(s, names[i], what)
	}
}

// outer is the span of n with its parentheses.
func outer(n ast.Element) ast.Span {
	if e, ok := n.(ast.Expr); ok {
		return e.OuterSpan()
	}
	return n.Span()
}

type spanFrame struct {
	n       ast.Element
	lastEnd int // the end of the last child
}

type spanVisitor struct {
	c     *spanChecker
	stack []spanFrame
}

func (v *spanVisitor) Visit(n ast.Element) ast.Visitor {
	if n == nil {
		v.stack = v.stack[:len(v.stack)-1]
		return nil
	}
	c := v.c
	c.checked++
	what := fmt.Sprintf("%T", n)
	s := n.Span()
	if len(v.stack) > 0 {
		f := &v.stack[len(v.stack)-1]
		o, ps := outer(n), f.n.Span()
		if o.Start < ps.Start || o.End > ps.End {
			c.errorf("%s %v (%v) is out of its parent %T %v", what, s, o, f.n, ps)
		}
		if o.Start < f.lastEnd {
			c.errorf("%s %v (%v) overlaps its previous sibling, ending at %d", what, s, o, f.lastEnd)
		}
		f.lastEnd = o.End
	}
	v.stack = append(v.stack, spanFrame{n: n})
	if _, ok := n.(*ast.Chunk); ok {
		return v
	}
	if !c.checkSpan(s, what) {
		return v
	}
	c.checkParens(n)
	c.checkElement(n)
	if c.reparse {
		c.reparseElement(n)
	}
	return v
}

// checkParens checks that the parentheses of an expression are Count "("
// tokens before it and Count ")" tokens after it.
func (c *spanChecker) checkParens(n ast.Element) {
	e, ok := n.(ast.Expr)
	if !ok {
		return
	}
	p, s := e.Parens(), e.Span()
	if p.Count == 0 {
		if e.OuterSpan() != s {
			c.errorf("%T: outer span %v without parentheses", n, e.OuterSpan())
		}
		return
	}
	if !c.checkSpan(p.Span, "parentheses") {
		return
	}
	i, j := c.byStart[p.Span.Start], c.byStart[s.Start]
	k, l := c.byEnd[s.End], c.byEnd[p.Span.End]
	if j-i != p.Count || l-k != p.Count {
		c.errorf("%T %q: %d parentheses in %q", n, c.text(s), p.Count, c.text(p.Span))
		return
	}
	for _, t := range c.tokens[i:j] {
		if t.typ != '(' {
			c.errorf("%T %q: parentheses %q", n, c.text(s), c.text(p.Span))
		}
	}
	for _, t := range c.tokens[k+1 : l+1] {
		if t.typ != ')' {
			c.errorf("%T %q: parentheses %q", n, c.text(s), c.text(p.Span))
		}
	}
}

// relex reads the text of a span as one token.
func (c *spanChecker) relex(s ast.Span) (t token, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	ls := newLexer(c.src[s.Start:s.End], "relex", c.opts.Dialect, false)
	ls.nextToken()
	return ls.t, ls.t.end == s.End-s.Start
}

var stmtKeywords = map[string]string{
	"*ast.LocalAssignStmt": "local", "*ast.DoBlockStmt": "do", "*ast.WhileStmt": "while",
	"*ast.RepeatStmt": "repeat", "*ast.NumberForStmt": "for", "*ast.GenericForStmt": "for",
	"*ast.FuncDefStmt": "function", "*ast.ReturnStmt": "return", "*ast.BreakStmt": "break",
	"*ast.GotoStmt": "goto", "*ast.LabelStmt": "::",
}

func (c *spanChecker) checkElement(n ast.Element) {
	s := n.Span()
	text := c.text(s)
	what := fmt.Sprintf("%T", n)
	first := c.tokens[c.byStart[s.Start]]
	if kw, ok := stmtKeywords[what]; ok && !strings.HasPrefix(text, kw) {
		c.errorf("%s starts with %q", what, text)
	}
	leaf := func(value string) {
		if text != value {
			c.errorf("%s %q spans %q", what, value, text)
		}
	}
	if ph, ok := n.(ast.PositionHolder); ok {
		switch n.(type) {
		case *ast.IdentExpr, *ast.NumberExpr, *ast.NilExpr, *ast.TrueExpr, *ast.FalseExpr,
			*ast.Comma3Expr, *ast.TableExpr, *ast.CdataNumberExpr, *ast.StringExpr:
			if n.(ast.Expr).Parens().Count == 0 && ph.Line() != first.line {
				c.errorf("%s %q: line %d, its token's %d", what, text, ph.Line(), first.line)
			}
		}
	}
	switch n := n.(type) {
	case *ast.IdentExpr:
		leaf(n.Value)
	case *ast.NilExpr:
		leaf("nil")
	case *ast.TrueExpr:
		leaf("true")
	case *ast.FalseExpr:
		leaf("false")
	case *ast.Comma3Expr:
		leaf("...")
	case *ast.CdataNumberExpr:
		leaf(n.Value)
	case *ast.NumberExpr:
		if t, ok := c.relex(s); !ok || t.typ != tkNumber || t.sem != n.Value {
			c.errorf("number %q spans %q", n.Value, text)
		}
	case *ast.StringExpr:
		if text[0] == '"' || text[0] == '\'' || text[0] == '[' {
			if t, ok := c.relex(s); !ok || t.typ != tkString || t.sem != n.Value {
				c.errorf("string %q spans %q", n.Value, text)
			}
		} else {
			leaf(n.Value)
		}
	case *ast.FuncCallExpr:
		if n.Receiver != nil {
			c.checkName(n.MethodSpan(), n.Method, "method")
		} else if n.MethodSpan() != (ast.Span{}) {
			c.errorf("call %q has a method span", text)
		}
	case *ast.FunctionExpr:
		if text[0] != '(' && !strings.HasPrefix(text, "function") || !strings.HasSuffix(text, "end") {
			c.errorf("function %q", text)
		}
		c.checkBlock(n.StmtsSpan, n.Stmts, "function")
	case *ast.ParList:
		c.checkNames(n.NameSpans, n.Names, "parameter")
		if text[0] != '(' || text[len(text)-1] != ')' {
			c.errorf("parameters %q", text)
		}
		if n.HasVargs {
			c.checkName(n.VarargSpan, "...", "vararg")
		} else if n.VarargSpan != (ast.Span{}) {
			c.errorf("parameters %q: vararg span", text)
		}
	case *ast.FuncName:
		if n.Receiver != nil {
			c.checkName(n.MethodSpan, n.Method, "method")
		}
	case *ast.LocalAssignStmt:
		c.checkNames(n.NameSpans, n.Names, "local")
	case *ast.NumberForStmt:
		c.checkName(n.NameSpan, n.Name, "for variable")
		c.checkBlock(n.StmtsSpan, n.Stmts, "for")
	case *ast.GenericForStmt:
		c.checkNames(n.NameSpans, n.Names, "for variable")
		c.checkBlock(n.StmtsSpan, n.Stmts, "for")
	case *ast.LabelStmt:
		c.checkName(n.NameSpan, n.Name, "label")
	case *ast.GotoStmt:
		c.checkName(n.LabelSpan, n.Label, "goto label")
	case *ast.DoBlockStmt:
		c.checkBlock(n.StmtsSpan, n.Stmts, "do")
	case *ast.WhileStmt:
		c.checkBlock(n.StmtsSpan, n.Stmts, "while")
	case *ast.RepeatStmt:
		c.checkBlock(n.StmtsSpan, n.Stmts, "repeat")
	case *ast.IfStmt:
		if !strings.HasPrefix(text, "if") && !strings.HasPrefix(text, "elseif") {
			c.errorf("if %q", text)
		}
		c.checkBlock(n.ThenSpan, n.Then, "then")
		if elseif, ok := c.elseif(n); ok {
			if n.ElseSpan != elseif.Span() {
				c.errorf("if %q: else span %v, elseif %v", text, n.ElseSpan, elseif.Span())
			}
		} else if len(n.Else) > 0 || n.ElseSpan != (ast.Span{}) { // the zero Span without an else
			c.checkBlock(n.ElseSpan, n.Else, "else")
		}
	}
}

// elseif returns the IfStmt of an "elseif" after n.
func (c *spanChecker) elseif(n *ast.IfStmt) (*ast.IfStmt, bool) {
	if len(n.Else) != 1 {
		return nil, false
	}
	st, ok := n.Else[0].(*ast.IfStmt)
	return st, ok && strings.HasPrefix(c.text(st.Span()), "elseif")
}

/* reparsing */

// fingerprint describes a tree without its lines and spans; the root's
// parentheses (outside its span) are left out.
func fingerprint(n ast.Element) string {
	var b strings.Builder
	root := true
	ast.Inspect(n, func(n ast.Element) bool {
		fmt.Fprintf(&b, "%T", n)
		if e, ok := n.(ast.Expr); ok && !root {
			fmt.Fprintf(&b, "(%d)", e.Parens().Count)
		}
		switch n := n.(type) {
		case *ast.IdentExpr:
			fmt.Fprintf(&b, "%q", n.Value)
		case *ast.StringExpr:
			fmt.Fprintf(&b, "%q", n.Value)
		case *ast.NumberExpr:
			fmt.Fprintf(&b, "%q", n.Value)
		case *ast.CdataNumberExpr:
			fmt.Fprintf(&b, "%q", n.Value)
		case *ast.Comma3Expr:
			if !root {
				fmt.Fprintf(&b, "%v", n.AdjustRet)
			}
		case *ast.FuncCallExpr:
			fmt.Fprintf(&b, "%q%d%v", n.Method, len(n.Args), n.Receiver != nil)
			if !root {
				fmt.Fprintf(&b, "%v", n.AdjustRet)
			}
		case *ast.LogicalOpExpr:
			b.WriteString(n.Operator)
		case *ast.RelationalOpExpr:
			b.WriteString(n.Operator)
		case *ast.ArithmeticOpExpr:
			b.WriteString(n.Operator)
		case *ast.TableExpr:
			fmt.Fprintf(&b, "%d", len(n.Fields))
		case *ast.Field:
			fmt.Fprintf(&b, "%v", n.Key != nil)
		case *ast.FunctionExpr:
			fmt.Fprintf(&b, "%d", len(n.Stmts))
		case *ast.ParList:
			fmt.Fprintf(&b, "%q%v%v", n.Names, n.HasVargs, n.NoArg)
		case *ast.FuncName:
			fmt.Fprintf(&b, "%q%v", n.Method, n.Receiver != nil)
		case *ast.AssignStmt:
			fmt.Fprintf(&b, "%d,%d", len(n.Lhs), len(n.Rhs))
		case *ast.LocalAssignStmt:
			fmt.Fprintf(&b, "%q%d%v", n.Names, len(n.Exprs), n.IsFunction)
		case *ast.IfStmt:
			fmt.Fprintf(&b, "%d,%d", len(n.Then), len(n.Else))
		case *ast.NumberForStmt:
			fmt.Fprintf(&b, "%q%v%d", n.Name, n.Step != nil, len(n.Stmts))
		case *ast.GenericForStmt:
			fmt.Fprintf(&b, "%q%d,%d", n.Names, len(n.Exprs), len(n.Stmts))
		case *ast.DoBlockStmt:
			fmt.Fprintf(&b, "%d", len(n.Stmts))
		case *ast.WhileStmt:
			fmt.Fprintf(&b, "%d", len(n.Stmts))
		case *ast.RepeatStmt:
			fmt.Fprintf(&b, "%d", len(n.Stmts))
		case *ast.ReturnStmt:
			fmt.Fprintf(&b, "%d", len(n.Exprs))
		case *ast.LabelStmt:
			fmt.Fprintf(&b, "%q", n.Name)
		case *ast.GotoStmt:
			fmt.Fprintf(&b, "%q", n.Label)
		}
		b.WriteByte(' ')
		root = false
		return true
	})
	return b.String()
}

// maxReparse is the longest text reparsed, which keeps the check linear.
const maxReparse = 400

// reparseElement parses the text of n in a context that accepts it and
// checks that it gives the same tree.
func (c *spanChecker) reparseElement(n ast.Element) {
	text := c.text(n.Span())
	if len(text) > maxReparse {
		return
	}
	var src string
	var pick func(*ast.Chunk) ast.Element
	// fn is the function expression of "return function(...) <body> end"
	fn := func(ch *ast.Chunk) *ast.FunctionExpr {
		if len(ch.Stmts) != 1 {
			return nil
		}
		r, ok := ch.Stmts[0].(*ast.ReturnStmt)
		if !ok || len(r.Exprs) != 1 {
			return nil
		}
		f, _ := r.Exprs[0].(*ast.FunctionExpr)
		return f
	}
	switch n.(type) {
	case *ast.IfStmt:
		if strings.HasPrefix(text, "elseif") {
			return // in its if
		}
		src = "return function(...) while true do\n" + text + "\nend end"
	case *ast.LabelStmt, *ast.GotoStmt:
		return // their labels are out of the text
	case *ast.StringExpr:
		if text[0] != '"' && text[0] != '\'' && text[0] != '[' {
			return // a name
		}
		src = "return function(...) return " + text + "\nend"
		pick = func(ch *ast.Chunk) ast.Element {
			if f := fn(ch); f != nil && len(f.Stmts) == 1 {
				if r, ok := f.Stmts[0].(*ast.ReturnStmt); ok && len(r.Exprs) == 1 {
					return r.Exprs[0]
				}
			}
			return nil
		}
	case ast.Stmt:
		src = "return function(...) while true do\n" + text + "\nend end"
		pick = func(ch *ast.Chunk) ast.Element {
			if f := fn(ch); f != nil && len(f.Stmts) == 1 {
				if w, ok := f.Stmts[0].(*ast.WhileStmt); ok && len(w.Stmts) == 1 {
					return w.Stmts[0]
				}
			}
			return nil
		}
	case *ast.FunctionExpr:
		if text[0] == '(' {
			text = "function" + text
		}
		src = "return " + text
		pick = func(ch *ast.Chunk) ast.Element { return fn(ch) }
	case ast.Expr:
		src = "return function(...) return " + text + "\nend"
		pick = func(ch *ast.Chunk) ast.Element {
			if f := fn(ch); f != nil && len(f.Stmts) == 1 {
				if r, ok := f.Stmts[0].(*ast.ReturnStmt); ok && len(r.Exprs) == 1 {
					return r.Exprs[0]
				}
			}
			return nil
		}
	case *ast.Field:
		src = "return {" + text + "}"
		pick = func(ch *ast.Chunk) ast.Element {
			if r, ok := ch.Stmts[0].(*ast.ReturnStmt); ok && len(r.Exprs) == 1 {
				if t, ok := r.Exprs[0].(*ast.TableExpr); ok && len(t.Fields) == 1 {
					return t.Fields[0]
				}
			}
			return nil
		}
	case *ast.ParList:
		src = "return function" + text + " end"
		pick = func(ch *ast.Chunk) ast.Element {
			if f := fn(ch); f != nil {
				return f.ParList
			}
			return nil
		}
	case *ast.FuncName:
		src = "function " + text + "() end"
		pick = func(ch *ast.Chunk) ast.Element {
			if d, ok := ch.Stmts[0].(*ast.FuncDefStmt); ok {
				return d.Name
			}
			return nil
		}
	default:
		return
	}
	if pick == nil { // an if: the first statement of the loop
		pick = func(ch *ast.Chunk) ast.Element {
			if f := fn(ch); f != nil && len(f.Stmts) == 1 {
				if w, ok := f.Stmts[0].(*ast.WhileStmt); ok && len(w.Stmts) == 1 {
					return w.Stmts[0]
				}
			}
			return nil
		}
	}
	ch, err := ParseChunk([]byte(src), "reparse", Options{Dialect: c.opts.Dialect})
	if err != nil {
		if hasGotoOrLabel(n) {
			return // a goto out of the text
		}
		c.errorf("%T %q does not parse alone: %v", n, text, err)
		return
	}
	got := pick(ch)
	if got == nil {
		c.errorf("%T %q parses alone to %s", n, text, fingerprint(ch))
		return
	}
	c.parsed++
	if a, b := fingerprint(n), fingerprint(got); a != b {
		c.errorf("%T %q parses alone to another tree:\n%s\n%s", n, text, a, b)
	}
}

func hasGotoOrLabel(n ast.Element) bool {
	found := false
	ast.Inspect(n, func(n ast.Element) bool {
		switch n.(type) {
		case *ast.GotoStmt, *ast.LabelStmt:
			found = true
		}
		return !found
	})
	return found
}
