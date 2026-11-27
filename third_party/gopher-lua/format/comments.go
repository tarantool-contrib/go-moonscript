package format

import (
	"bytes"
	"sort"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
)

// Place is where a comment goes relative to the element it is attached
// to.
type Place uint8

const (
	// Leading: before the element, on the lines above it or on its line
	// before it.
	Leading Place = iota
	// Trailing: after the element, on the line it ends on.
	Trailing
	// Inner: inside the element, between its own tokens (keywords,
	// punctuation, the names held as strings) where no child element
	// starts or ends: in an empty block, table, parameter or argument list,
	// at the end of a block, before a keyword.
	Inner
)

func (p Place) String() string {
	switch p {
	case Leading:
		return "leading"
	case Trailing:
		return "trailing"
	case Inner:
		return "inner"
	}
	return "?"
}

// A Comment is a comment of the source and where it goes.
type Comment struct {
	ast.Comment
	// Raw is the comment as written, from its "--" to its last byte.
	Raw string
	// Place is where the comment goes relative to its element.
	Place Place
	// SameLine is set if the comment starts on the line of the token
	// before it (no line break between them).
	SameLine bool
	// BlankBefore is set if an empty line separates the comment from the
	// token or comment before it.
	BlankBefore bool
	// NewlineAfter is set if a line break follows the comment before the
	// next token or comment (always for a short comment).
	NewlineAfter bool
	// Index and Next say where an Inner comment goes in its element: after
	// Index of its children (those starting before it, in source order:
	// the order of ast.Walk), before the token of kind Next (TokenEOF at
	// the end of the chunk).
	Index int
	Next  ast.TokenKind
}

// A CommentMap maps the elements of a tree to the comments attached to
// them, in source order, as go/ast.CommentMap does. Every comment of the
// source is attached to exactly one element, so that a printer that
// prints each element's comments with it prints each comment once.
type CommentMap map[ast.Element][]*Comment

// Comments returns the comments of the map in source order (by their
// spans; comments without one last, in no particular order).
func (cm CommentMap) Comments() []*Comment {
	var list []*Comment
	for _, cs := range cm {
		list = append(list, cs...)
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i].Span, list[j].Span
		if (a == ast.Span{}) != (b == ast.Span{}) {
			return b == ast.Span{}
		}
		return a.Start < b.Start
	})
	return list
}

// gap is a run of trivia holding comments, between the grammar tokens p
// (-1: the start of the source) and n (TokenEOF at the end).
type gap struct {
	p, n     int
	comments []*Comment
}

// NewCommentMap attaches the comments of a chunk parsed with its tokens
// (parse.Options.Tokens) to its elements; it returns nil if the chunk has
// no tokens or no comments. For a comment, let P be the token of the
// grammar before it and N the one after it, PE the outermost element
// ending with P (with its parentheses; P being a "," or ";", the token
// before it) and NE the outermost element starting with N (with its
// parentheses; else, a comment inside parentheses, without them). The
// comments between P and N before the first line break between them are
// on P's line, the others on lines of their own. A comment is attached:
//
//   - on P's line and followed by a line break: Trailing to PE, if any,
//     else Leading to NE (with SameLine: "{ -- c", "then -- c"), else
//     Inner;
//   - on P's line and followed by N on the same line: Leading to NE, if
//     any ("x = --[[c]] 1"), else Trailing to PE, else Inner;
//   - on a line of its own: Leading to NE, if any, else Inner;
//   - Inner: to the innermost element holding it (the chunk at its end
//     or in an empty chunk), with the index of the children before it and
//     the kind of N: the end of a block (before "end", "else", "until",
//     the end of the chunk), an empty table, parameter or argument list,
//     before a keyword, a "," or a name held as a string.
//
// So a comment at the end of a line goes with what the line ends with,
// one on lines above a statement, a field or an argument goes with it,
// and one at the end of a block, or in an empty block or list, with the
// element holding the block or list.
func NewCommentMap(chunk *ast.Chunk) CommentMap {
	if chunk == nil || len(chunk.Tokens) == 0 || chunk.Lines == nil {
		return nil
	}
	src := chunk.Lines.Source()
	toks := chunk.Tokens
	var gaps []gap
	prev := -1
	var cur []*Comment
	nl, broke := 0, false // line breaks since the last token or comment; since prev
	var last *Comment
	for i, t := range toks {
		switch t.Kind {
		case ast.TokenNewline:
			nl++
			broke = true
			if last != nil {
				last.NewlineAfter = true
			}
			continue
		case ast.TokenWhitespace, ast.TokenHeader:
			if t.Kind == ast.TokenHeader {
				broke = true
			}
			continue
		case ast.TokenComment:
			s := t.Span()
			raw := string(src[s.Start:s.End])
			c := &Comment{Raw: raw}
			c.Span = s
			c.Text, c.Long = commentText(raw)
			c.NewlineAfter = !c.Long
			c.SameLine = prev >= 0 && !broke
			c.BlankBefore = nl >= 2
			cur = append(cur, c)
			last = c
			nl = 0
			if c.Long && bytes.ContainsAny(src[s.Start:s.End], "\r\n") {
				broke = true
			}
			continue
		}
		if len(cur) > 0 {
			gaps = append(gaps, gap{p: prev, n: i, comments: cur})
			cur = nil
		}
		prev, nl, broke, last = i, 0, false, nil
	}
	if len(cur) > 0 { // tokens without EOF
		gaps = append(gaps, gap{p: prev, n: len(toks), comments: cur})
	}
	if len(gaps) == 0 {
		return nil
	}

	// the elements ending and starting at the offsets of the gaps
	type ends struct{ outer, inner ast.Element }
	endAt := map[int]*ends{}
	startAt := map[int]*ends{}
	prevGrammar := func(i int) int {
		for i--; i >= 0 && toks[i].Kind.IsTrivia(); i-- {
		}
		return i
	}
	pEnd := make([]int, len(gaps))
	nStart := make([]int, len(gaps))
	for gi, g := range gaps {
		pEnd[gi], nStart[gi] = -1, -1
		if q := g.p; q >= 0 {
			if toks[q].Kind == ast.TokenComma {
				q = prevGrammar(q)
			} else {
				for q >= 0 && toks[q].Kind == ast.TokenSemicolon {
					q = prevGrammar(q)
				}
			}
			if q >= 0 {
				pEnd[gi] = toks[q].Span().End
				endAt[pEnd[gi]] = &ends{}
			}
		}
		if g.n < len(toks) && toks[g.n].Kind != ast.TokenEOF {
			nStart[gi] = toks[g.n].Span().Start
			startAt[nStart[gi]] = &ends{}
		}
	}
	// the offsets, sorted: an element without one in its span, nor its
	// children, ends or starts at none
	offs := make([]int, 0, len(endAt)+len(startAt))
	for o := range endAt {
		offs = append(offs, o)
	}
	for o := range startAt {
		offs = append(offs, o)
	}
	sort.Ints(offs)
	var visit func(el ast.Element)
	visit = func(el ast.Element) {
		s := el.Span()
		if s == (ast.Span{}) {
			forEachChild(el, visit)
			return
		}
		o := outerSpan(el)
		lo, hi := min(o.Start, s.Start), max(o.End, s.End)
		if i := sort.SearchInts(offs, lo); i == len(offs) || offs[i] > hi {
			return
		}
		// a parent before its children: the outermost first
		if el != ast.Element(chunk) {
			if e := endAt[o.End]; e != nil && e.outer == nil {
				e.outer = el
			}
			if e := endAt[s.End]; e != nil && e.inner == nil {
				e.inner = el
			}
			if e := startAt[o.Start]; e != nil && e.outer == nil {
				e.outer = el
			}
			if e := startAt[s.Start]; e != nil && e.inner == nil {
				e.inner = el
			}
		}
		forEachChild(el, visit)
	}
	visit(chunk)
	pick := func(m map[int]*ends, off int) ast.Element {
		if off < 0 {
			return nil
		}
		e := m[off]
		if e.outer != nil {
			return e.outer
		}
		return e.inner
	}

	cm := CommentMap{}
	attach := func(el ast.Element, c *Comment, place Place) {
		c.Place = place
		if place == Inner {
			c.Index, c.Next = 0, ast.TokenEOF
		}
		cm[el] = append(cm[el], c)
	}
	inner := func(g gap, c *Comment) {
		el, index := container(chunk, c.Span.Start)
		c.Place = Inner
		c.Index = index
		c.Next = ast.TokenEOF
		if g.n < len(toks) {
			c.Next = toks[g.n].Kind
		}
		cm[el] = append(cm[el], c)
	}
	for gi, g := range gaps {
		pe, ne := pick(endAt, pEnd[gi]), pick(startAt, nStart[gi])
		// the comments on P's line
		k := 0
		for k < len(g.comments) && g.comments[k].SameLine {
			k++
		}
		if k > 0 {
			lastSame := g.comments[k-1]
			brokeAfter := k < len(g.comments) || lastSame.NewlineAfter
			for _, c := range g.comments[:k] {
				switch {
				case brokeAfter && pe != nil:
					attach(pe, c, Trailing)
				case ne != nil:
					attach(ne, c, Leading)
				case pe != nil:
					attach(pe, c, Trailing)
				default:
					inner(g, c)
				}
			}
		}
		for _, c := range g.comments[k:] {
			if ne != nil {
				attach(ne, c, Leading)
			} else {
				inner(g, c)
			}
		}
	}
	return cm
}

// commentText returns the text of a comment as written, as ast.Comment
// has it, and whether it is long.
func commentText(raw string) (string, bool) {
	body := raw[2:]
	level := ast.LongBracketLevel([]byte(body))
	if level < 0 {
		return body, false
	}
	open := level + 2
	close := level + 2
	if len(body) < open+close {
		return "", true
	}
	t := body[open : len(body)-close]
	// a line break right after the opening bracket is skipped
	if len(t) > 0 && (t[0] == '\n' || t[0] == '\r') {
		if len(t) > 1 && (t[1] == '\n' || t[1] == '\r') && t[1] != t[0] {
			t = t[2:]
		} else {
			t = t[1:]
		}
	}
	return normalizeNewlines(t), true
}

// normalizeNewlines turns each line break ("\n", "\r", "\r\n", "\n\r")
// into "\n".
func normalizeNewlines(s string) string {
	if !bytes.ContainsAny([]byte(s), "\r") {
		return s
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\r' {
			if i+1 < len(s) && (s[i+1] == '\n' || s[i+1] == '\r') && s[i+1] != c {
				i++
			}
			b = append(b, '\n')
			continue
		}
		b = append(b, c)
	}
	return string(b)
}

// container returns the innermost element of the chunk whose span holds
// the offset (strictly inside it), and the number of its children that
// start before the offset.
func container(chunk *ast.Chunk, off int) (ast.Element, int) {
	var el ast.Element = chunk
	for {
		var next ast.Element
		index := 0
		forEachChild(el, func(c ast.Element) {
			s := c.Span()
			if s == (ast.Span{}) {
				return
			}
			if s.Start < off {
				index++
			}
			if next == nil && s.Start < off && off < s.End {
				next = c
			}
		})
		if next == nil {
			return el, index
		}
		el = next
	}
}

// outerSpan is the span of an element with the parentheses around it.
func outerSpan(el ast.Element) ast.Span {
	if e, ok := el.(ast.Expr); ok {
		return e.OuterSpan()
	}
	return el.Span()
}

// forEachChild calls f for the children of el, as ast.Walk has them,
// skipping nils.
func forEachChild(el ast.Element, f func(ast.Element)) {
	stmts := func(list []ast.Stmt) {
		for _, s := range list {
			if !isNil(s) {
				f(s)
			}
		}
	}
	exprs := func(list []ast.Expr) {
		for _, e := range list {
			if !isNil(e) {
				f(e)
			}
		}
	}
	expr := func(e ast.Expr) {
		if !isNil(e) {
			f(e)
		}
	}
	switch n := el.(type) {
	case *ast.Chunk:
		stmts(n.Stmts)
	case *ast.AssignStmt:
		exprs(n.Lhs)
		exprs(n.Rhs)
	case *ast.LocalAssignStmt:
		exprs(n.Exprs)
	case *ast.FuncCallStmt:
		expr(n.Expr)
	case *ast.DoBlockStmt:
		stmts(n.Stmts)
	case *ast.WhileStmt:
		expr(n.Condition)
		stmts(n.Stmts)
	case *ast.RepeatStmt:
		stmts(n.Stmts)
		expr(n.Condition)
	case *ast.IfStmt:
		expr(n.Condition)
		stmts(n.Then)
		stmts(n.Else)
	case *ast.NumberForStmt:
		expr(n.Init)
		expr(n.Limit)
		expr(n.Step)
		stmts(n.Stmts)
	case *ast.GenericForStmt:
		exprs(n.Exprs)
		stmts(n.Stmts)
	case *ast.FuncDefStmt:
		if n.Name != nil {
			f(n.Name)
		}
		if n.Func != nil {
			f(n.Func)
		}
	case *ast.ReturnStmt:
		exprs(n.Exprs)
	case *ast.AttrGetExpr:
		expr(n.Object)
		expr(n.Key)
	case *ast.TableExpr:
		for _, fl := range n.Fields {
			if fl != nil {
				f(fl)
			}
		}
	case *ast.Field:
		expr(n.Key)
		expr(n.Value)
	case *ast.FuncCallExpr:
		expr(n.Func)
		expr(n.Receiver)
		exprs(n.Args)
	case *ast.LogicalOpExpr:
		expr(n.Lhs)
		expr(n.Rhs)
	case *ast.RelationalOpExpr:
		expr(n.Lhs)
		expr(n.Rhs)
	case *ast.StringConcatOpExpr:
		expr(n.Lhs)
		expr(n.Rhs)
	case *ast.ArithmeticOpExpr:
		expr(n.Lhs)
		expr(n.Rhs)
	case *ast.UnaryMinusOpExpr:
		expr(n.Expr)
	case *ast.UnaryNotOpExpr:
		expr(n.Expr)
	case *ast.UnaryLenOpExpr:
		expr(n.Expr)
	case *ast.FunctionExpr:
		if n.ParList != nil {
			f(n.ParList)
		}
		stmts(n.Stmts)
	case *ast.FuncName:
		expr(n.Func)
		expr(n.Receiver)
	}
}

// isNil reports whether an element is nil or a nil pointer of the types
// of package ast.
func isNil(el any) bool {
	switch n := el.(type) {
	case nil:
		return true
	case *ast.AssignStmt:
		return n == nil
	case *ast.LocalAssignStmt:
		return n == nil
	case *ast.FuncCallStmt:
		return n == nil
	case *ast.DoBlockStmt:
		return n == nil
	case *ast.WhileStmt:
		return n == nil
	case *ast.RepeatStmt:
		return n == nil
	case *ast.IfStmt:
		return n == nil
	case *ast.NumberForStmt:
		return n == nil
	case *ast.GenericForStmt:
		return n == nil
	case *ast.FuncDefStmt:
		return n == nil
	case *ast.ReturnStmt:
		return n == nil
	case *ast.BreakStmt:
		return n == nil
	case *ast.LabelStmt:
		return n == nil
	case *ast.GotoStmt:
		return n == nil
	case *ast.TrueExpr:
		return n == nil
	case *ast.FalseExpr:
		return n == nil
	case *ast.NilExpr:
		return n == nil
	case *ast.NumberExpr:
		return n == nil
	case *ast.StringExpr:
		return n == nil
	case *ast.CdataNumberExpr:
		return n == nil
	case *ast.Comma3Expr:
		return n == nil
	case *ast.IdentExpr:
		return n == nil
	case *ast.AttrGetExpr:
		return n == nil
	case *ast.TableExpr:
		return n == nil
	case *ast.FuncCallExpr:
		return n == nil
	case *ast.LogicalOpExpr:
		return n == nil
	case *ast.RelationalOpExpr:
		return n == nil
	case *ast.StringConcatOpExpr:
		return n == nil
	case *ast.ArithmeticOpExpr:
		return n == nil
	case *ast.UnaryMinusOpExpr:
		return n == nil
	case *ast.UnaryNotOpExpr:
		return n == nil
	case *ast.UnaryLenOpExpr:
		return n == nil
	case *ast.FunctionExpr:
		return n == nil
	case *ast.Field:
		return n == nil
	case *ast.ParList:
		return n == nil
	case *ast.FuncName:
		return n == nil
	case *ast.Chunk:
		return n == nil
	}
	return false
}
