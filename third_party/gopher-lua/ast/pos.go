package ast

// go-moonscript patch: positions in the source (see doc.go).

import (
	"bytes"
	"fmt"
	"sort"
	"sync"
)

// Span is where something lies in the source: the bytes src[Start:End],
// byte offsets from the start of the source. A node without a position
// has the zero Span.
type Span struct {
	Start int
	End   int
}

// String returns "start-end".
func (s Span) String() string {
	return fmt.Sprintf("%d-%d", s.Start, s.End)
}

// Pos is a position in the source: a byte offset (0-based), a line
// (1-based, lines counted as the lexer counts them: "\n", "\r", "\r\n" and
// "\n\r" end a line) and a column (1-based, counted in bytes: a tab or a
// byte of a multi-byte UTF-8 character is one column). The zero Pos is no
// position.
type Pos struct {
	Offset int
	Line   int
	Column int
}

// IsValid reports whether p is a position (its line is not 0).
func (p Pos) IsValid() bool { return p.Line > 0 }

// String returns "line:column", or "-" for no position.
func (p Pos) String() string {
	if !p.IsValid() {
		return "-"
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// Lines gives the line and column of the byte offsets of a source. It
// finds the starts of the lines on the first call that needs them, once,
// and is then safe for concurrent use; the source must not change.
type Lines struct {
	once   sync.Once
	src    []byte
	starts []int // the offsets of the lines' first bytes
}

// NewLines returns the Lines of src, which it keeps (not a copy).
func NewLines(src []byte) *Lines {
	return &Lines{src: src}
}

func (l *Lines) init() {
	l.once.Do(func() {
		src := l.src
		starts := make([]int, 1, 1+len(src)/32)
		for i := 0; ; {
			j := bytes.IndexAny(src[i:], "\n\r")
			if j < 0 {
				break
			}
			i += j
			c := src[i]
			i++
			// "\r\n" and "\n\r" are one line break (inclinenumber)
			if i < len(src) && (src[i] == '\n' || src[i] == '\r') && src[i] != c {
				i++
			}
			starts = append(starts, i)
		}
		l.starts = starts
	})
}

// Source returns the source of the offsets, as NewLines was given it.
func (l *Lines) Source() []byte {
	return l.src
}

// Count returns the number of lines of the source: 1 and one per line
// break.
func (l *Lines) Count() int {
	l.init()
	return len(l.starts)
}

// Start returns the offset of the first byte of the line, or -1 if the
// source has no such line.
func (l *Lines) Start(line int) int {
	l.init()
	if line < 1 || line > len(l.starts) {
		return -1
	}
	return l.starts[line-1]
}

// Pos returns the position of the byte at offset, which is clamped to the
// source (len(src) is the position after its last byte). A line break of
// two bytes is on the line it ends.
func (l *Lines) Pos(offset int) Pos {
	l.init()
	offset = max(0, min(offset, len(l.src)))
	i := sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > offset }) - 1
	return Pos{Offset: offset, Line: i + 1, Column: offset - l.starts[i] + 1}
}

// Positions returns the positions of the start and the end of the span
// (the end is the position after its last byte), or two zero Pos for the
// zero Span, that of a node without a position.
func (l *Lines) Positions(s Span) (start, end Pos) {
	if s == (Span{}) {
		return Pos{}, Pos{}
	}
	return l.Pos(s.Start), l.Pos(s.End)
}
