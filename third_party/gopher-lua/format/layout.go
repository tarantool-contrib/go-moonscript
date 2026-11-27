package format

// The layout: a document of texts, line breaks, groups and indentation,
// laid out within a width as Wadler's "prettier printer" and Oppen's
// algorithm do, in the variant of JavaScript's Prettier (a stack machine
// that measures a group in flat mode against the rest of the line). The
// printer builds the document from the tree; render lays it out.

import (
	"unicode/utf8"
)

// doc is a document: text, num, space, *concat, *group, *nest, line,
// blank, *ifBreak, *choice, *comment, or nil (nothing).
type doc any

// text is a piece of text; a long string or comment may hold line breaks.
type text string

// num is a numeral: text after which a "." or a name character needs a
// space.
type num string

// concat is a sequence of documents; hard is set if one of them is.
type concat struct {
	parts []doc
	hard  bool
}

// group is laid out flat (its lines as spaces or nothing) if it fits in
// the rest of the line, else broken (its lines as line breaks, its groups
// laid out in turn). A hard group is always broken: it holds a hard line
// break, or a comment that ends a line.
type group struct {
	d    doc
	hard bool
	// strict: flat only if it fits flat whole, a call's arguments
	// hugging their last one included (a block on one line)
	strict bool
}

// nest indents the lines in it one level more.
type nest struct {
	d    doc
	hard bool
}

// line is a line break of its group.
type line uint8

const (
	lineSpace line = iota // a space if flat
	lineSoft              // nothing if flat
	lineHard              // always a line break
)

// blank asks for an empty line before the next text, if its group is
// broken: a blank line of the source, kept.
type blank struct{}

// ifBreak is broken or flat, as its group is laid out.
type ifBreak struct {
	broken, flat doc
}

// choice is a call's arguments, laid out as the first of its options that
// fits (Prettier's conditional group of the arguments): expanded flat,
// else hug broken up to its first line break, else expanded broken.
// Unlike a group, it does not break the groups around it when expanded
// holds a hard line break: hug, which then fits up to its line break, may
// open on their line.
type choice struct {
	expanded doc
	hugs     []doc
}

// flat lays out its document flat (its hard line breaks broken).
type flat struct {
	d doc
}

// comment is a comment as written (raw), short or long. It goes on its
// own line unless sameLine, after an empty line if blank, and the next
// text goes on a new line if short or nlAfter.
type comment struct {
	raw                           string
	short, sameLine, blank, nlAft bool
}

func (c *comment) isHard() bool { return c.short || c.nlAft || !c.sameLine }

// isHard reports whether d holds a hard line break.
func isHard(d doc) bool {
	switch d := d.(type) {
	case *concat:
		return d.hard
	case *group:
		return d.hard
	case *nest:
		return d.hard
	case *flat:
		return isHard(d.d)
	case line:
		return d == lineHard
	case *comment:
		return d.isHard()
	case *ifBreak:
		return isHard(d.broken) || isHard(d.flat)
	}
	return false
}

// cat concatenates documents, leaving out nils and flattening concats.
func cat(ds ...doc) doc {
	n, last := 0, doc(nil)
	for _, d := range ds {
		if d != nil {
			n++
			last = d
		}
	}
	switch n {
	case 0:
		return nil
	case 1:
		return last
	}
	var c *concat
	if n <= 4 { // the concat and its parts in one allocation
		s := &struct {
			c concat
			a [4]doc
		}{}
		c = &s.c
		c.parts = s.a[:0]
	} else {
		c = &concat{parts: make([]doc, 0, n)}
	}
	for _, d := range ds {
		if d == nil {
			continue
		}
		c.parts = append(c.parts, d)
		if !c.hard && isHard(d) {
			c.hard = true
		}
	}
	return c
}

// catList is cat of a list it may keep.
func catList(ds []doc) doc {
	return cat(ds...)
}

func mkGroup(d doc) doc {
	if d == nil {
		return nil
	}
	return &group{d: d, hard: isHard(d)}
}

// forceBreak returns d with its group always broken: d, or the last part
// of d (a function's body).
func forceBreak(d doc) doc {
	switch g := d.(type) {
	case *group:
		return &group{d: g.d, hard: true}
	case *concat:
		if len(g.parts) > 0 {
			if last, ok := g.parts[len(g.parts)-1].(*group); ok {
				parts := append(append([]doc(nil), g.parts[:len(g.parts)-1]...), &group{d: last.d, hard: true})
				return &concat{parts: parts, hard: true}
			}
		}
	}
	return d
}

func mkNest(d doc) doc {
	if d == nil {
		return nil
	}
	return &nest{d: d, hard: isHard(d)}
}

// mode is how a group is laid out.
type mode uint8

const (
	modeBreak mode = iota
	modeFlat
)

type cmd struct {
	ind  int
	mode mode
	d    doc
}

// mcmd is a command being measured, in a strict group or not, of what is
// measured or of the rest of the line.
type mcmd struct {
	cmd
	strict, rest bool
}

// renderer lays out a document.
type renderer struct {
	width    int
	indent   string // one level of indentation
	indentW  int    // its width
	out      []byte
	col      int  // the width of the current line
	empty    bool // nothing on the current line yet (but the pending indentation)
	ind      int  // the indentation of the current line, written with its first text
	nlRun    int  // the line breaks at the end of out
	needNL   bool // the next text goes on a new line (after a short comment)
	nlInd    int  // its indentation, the comment's
	needSp   bool // a space goes before the next text
	wantBlk  bool // an empty line goes before the next text
	lastNum  bool // the last text was a numeral
	cmds     []cmd
	fitStack []mcmd
	// measuring is the depth of the measures under way
	measuring int
}

func newRenderer(width int, indent string, indentW int, size int) *renderer {
	return &renderer{width: width, indent: indent, indentW: indentW, empty: true, out: make([]byte, 0, size)}
}

func (r *renderer) newline(ind int) {
	if r.nlRun < 2 {
		r.out = append(r.out, '\n')
		r.nlRun++
	}
	r.col = ind * r.indentW
	r.ind = ind
	r.empty = true
	r.needNL = false
	r.needSp = false
}

// glue reports whether a space must separate a and b so that they are not
// read as one token: names and keywords, a numeral and a "." or a name
// character, "-" and "-" (a comment), "[" and "[" or "=" (a long
// bracket), "." and "." or a digit, and an operator and "=".
func (r *renderer) glue(b byte) bool {
	if len(r.out) == 0 {
		return false
	}
	a := r.out[len(r.out)-1]
	switch {
	case isWordByte(a) && isWordByte(b):
		return true
	case r.lastNum && (b == '.' || isWordByte(b)):
		return true
	case a == '-' && b == '-':
		return true
	case a == '[' && (b == '[' || b == '='):
		return true
	case a == '.' && (b == '.' || isDigit(b)):
		return true
	case (a == '=' || a == '<' || a == '>' || a == '~') && b == '=':
		return true
	case a == ':' && b == ':':
		return true
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b >= 0x80
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// write writes a text at indentation ind.
func (r *renderer) write(s string, ind int, isNum bool) {
	if s == "" {
		return
	}
	if r.needNL {
		r.newline(r.nlInd)
	}
	if r.wantBlk {
		if !r.empty {
			r.newline(ind)
		}
		if r.nlRun < 2 && len(r.out) > 0 {
			r.out = append(r.out, '\n')
			r.nlRun++
		}
		r.wantBlk = false
	}
	if r.empty {
		for i := 0; i < r.ind; i++ {
			r.out = append(r.out, r.indent...)
		}
		r.empty = false
	} else if r.needSp || r.glue(s[0]) {
		r.out = append(r.out, ' ')
		r.col++
	}
	r.needSp = false
	r.out = append(r.out, s...)
	r.nlRun = 0
	r.lastNum = isNum
	if i := lastLineBreak(s); i >= 0 {
		r.col = utf8.RuneCountInString(s[i+1:])
	} else {
		r.col += utf8.RuneCountInString(s)
	}
}

func lastLineBreak(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' || s[i] == '\r' {
			return i
		}
	}
	return -1
}

func firstLineBreak(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return i
		}
	}
	return -1
}

func (r *renderer) writeComment(c *comment, ind int) {
	if !c.sameLine || r.needNL {
		if !r.empty {
			r.newline(ind)
		}
		if c.blank {
			r.wantBlk = true
		}
	} else if !r.empty {
		r.needSp = true
	}
	r.write(c.raw, ind, false)
	if c.short || c.nlAft {
		r.needNL, r.nlInd = true, ind
	} else {
		r.needSp = true
	}
}

func (r *renderer) push(ind int, m mode, d doc) {
	r.cmds = append(r.cmds, cmd{ind, m, d})
}

// render lays out d.
func (r *renderer) render(d doc) {
	r.push(0, modeBreak, d)
	for len(r.cmds) > 0 {
		c := r.cmds[len(r.cmds)-1]
		r.cmds = r.cmds[:len(r.cmds)-1]
		switch d := c.d.(type) {
		case nil:
		case text:
			r.write(string(d), c.ind, false)
		case num:
			r.write(string(d), c.ind, true)
		case *concat:
			for i := len(d.parts) - 1; i >= 0; i-- {
				r.push(c.ind, c.mode, d.parts[i])
			}
		case *nest:
			// flat, its lines are spaces: the line breaks in it are those
			// of the arguments hugged, at the indentation of the line
			if c.mode == modeFlat {
				r.push(c.ind, c.mode, d.d)
			} else {
				r.push(c.ind+1, c.mode, d.d)
			}
		case *group:
			switch {
			case c.mode == modeFlat && !d.hard:
				r.push(c.ind, modeFlat, d.d)
			case !d.hard && r.fits(cmd{c.ind, modeFlat, d.d}, d.strict):
				r.push(c.ind, modeFlat, d.d)
			default:
				r.push(c.ind, modeBreak, d.d)
			}
		case *choice:
			switch {
			case !isHard(d.expanded) && r.fits(cmd{c.ind, modeFlat, d.expanded}, false):
				r.push(c.ind, modeFlat, d.expanded)
			default:
				chosen := d.expanded
				for _, h := range d.hugs {
					if r.fits(cmd{c.ind, modeFlat, h}, false) {
						chosen = h
						break
					}
				}
				r.push(c.ind, modeBreak, chosen)
			}
		case *flat:
			r.push(c.ind, modeFlat, d.d)
		case *ifBreak:
			if c.mode == modeBreak {
				r.push(c.ind, c.mode, d.broken)
			} else {
				r.push(c.ind, c.mode, d.flat)
			}
		case line:
			if c.mode == modeFlat && d != lineHard {
				if r.needNL {
					r.newline(r.nlInd)
				} else if d == lineSpace {
					r.needSp = true
				}
			} else {
				r.newline(c.ind)
			}
		case blank:
			if c.mode == modeBreak {
				r.wantBlk = true
			}
		case space:
			if !r.empty {
				r.needSp = true
			}
		case *comment:
			r.writeComment(d, c.ind)
		default:
			panic("format: unknown document")
		}
	}
}

// fits reports whether next fits in the rest of the line, followed by
// the commands on the stack up to their first line break; strict: next
// fits flat whole.
func (r *renderer) fits(next cmd, strict bool) bool {
	width := r.width - r.col
	if r.needNL {
		width = r.width - r.nlInd*r.indentW
	} else if r.needSp {
		width--
	}
	return r.measure(next, width, true, strict)
}

// fitsLine reports whether c fits in width up to its first line break.
func (r *renderer) fitsLine(c cmd, width int) bool {
	return r.measure(c, width, false, false)
}

// measure reports whether next fits in width, followed (withRest) by the
// commands on the stack, up to the first line break; without the rest,
// next must have a line break. In a strict group (next if strict), the
// arguments of a call fit only flat.
func (r *renderer) measure(next cmd, width int, withRest, strict bool) bool {
	var st []mcmd
	if r.measuring == 0 {
		st = r.fitStack[:0]
	}
	r.measuring++
	defer func() {
		r.measuring--
		if r.measuring == 0 {
			r.fitStack = st[:0]
		}
	}()
	st = append(st, mcmd{next, strict, false})
	rest := 0
	if withRest {
		rest = len(r.cmds)
	}
	for width >= 0 {
		if len(st) == 0 {
			if rest == 0 {
				return withRest
			}
			rest--
			st = append(st, mcmd{r.cmds[rest], false, true})
			continue
		}
		mc := st[len(st)-1]
		st = st[:len(st)-1]
		c, strict, inRest := mc.cmd, mc.strict, mc.rest
		switch d := c.d.(type) {
		case nil:
		case text:
			if i := firstLineBreak(string(d)); i >= 0 {
				return width-utf8.RuneCountInString(string(d[:i])) >= 0
			}
			width -= utf8.RuneCountInString(string(d))
		case num:
			width -= len(d)
		case *concat:
			for i := len(d.parts) - 1; i >= 0; i-- {
				st = append(st, mcmd{cmd{c.ind, c.mode, d.parts[i]}, strict, inRest})
			}
		case *nest:
			st = append(st, mcmd{cmd{c.ind + 1, c.mode, d.d}, strict, inRest})
		case *group:
			if strict && d.hard {
				return false
			}
			// a group of the rest of the line is measured up to its
			// first line break, a group of next flat
			m := modeFlat
			if inRest {
				m = c.mode
			}
			if d.hard {
				m = modeBreak
			}
			st = append(st, mcmd{cmd{c.ind, m, d.d}, strict || d.strict && m == modeFlat, inRest})
		case *choice:
			// the arguments fit if hug fits up to its line break, as it
			// is then laid out (render), or if expanded does
			// (expanded broken breaks a group being laid flat)
			if !strict {
				for _, h := range d.hugs {
					if r.fitsLine(cmd{c.ind, modeFlat, h}, width) {
						return true
					}
				}
			}
			if isHard(d.expanded) && (strict || c.mode == modeFlat) {
				return false
			}
			st = append(st, mcmd{cmd{c.ind, c.mode, d.expanded}, strict, inRest})
		case *flat:
			st = append(st, mcmd{cmd{c.ind, modeFlat, d.d}, strict, inRest})
		case *ifBreak:
			if c.mode == modeBreak {
				st = append(st, mcmd{cmd{c.ind, c.mode, d.broken}, strict, inRest})
			} else {
				st = append(st, mcmd{cmd{c.ind, c.mode, d.flat}, strict, inRest})
			}
		case line:
			if c.mode == modeBreak || d == lineHard {
				return true
			}
			if d == lineSpace {
				width--
			}
		case blank:
		case space:
			width--
		case *comment:
			if !d.sameLine {
				return true
			}
			raw := d.raw
			if i := firstLineBreak(raw); i >= 0 {
				return width-1-utf8.RuneCountInString(raw[:i]) >= 0
			}
			width -= 1 + utf8.RuneCountInString(raw)
			if d.short || d.nlAft {
				return width >= 0
			}
		}
	}
	return false
}
