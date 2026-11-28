package ast

import "math"

// PositionHolder is what every statement and expression has: the lines
// the compiler gives its instructions (see doc.go).
type PositionHolder interface {
	Line() int
	SetLine(int)
	LastLine() int
	SetLastLine(int)
}

// Element is a node of a syntax tree that has a place in the source: a
// statement, an expression, a table field, a parameter list, a function
// name or the chunk. go-moonscript patch.
type Element interface {
	// Span is where the node lies in the source: from the start of its
	// first token to the end of its last one. It is the zero Span when
	// the node has no position (a node built by other code).
	Span() Span
	SetSpan(Span)
}

// SpanBase holds the span of an element; every element embeds it. The
// span is kept in two 32-bit offsets, so an element of a source of 4 GiB
// or more has the zero Span. go-moonscript patch.
type SpanBase struct {
	start uint32
	end   uint32
}

// Span returns where the element lies in the source, the zero Span if
// unknown.
func (b *SpanBase) Span() Span {
	return Span{Start: int(b.start), End: int(b.end)}
}

// SetSpan sets where the element lies in the source. A span that is not
// one (a negative start, an end before the start) or that ends past 4 GiB
// is kept as the zero Span.
func (b *SpanBase) SetSpan(s Span) {
	if s.Start < 0 || s.End < s.Start || uint64(s.End) > math.MaxUint32 {
		b.start, b.end = 0, 0
		return
	}
	b.start, b.end = uint32(s.Start), uint32(s.End)
}

// Node holds the lines and the span of a statement or an expression
// (go-moonscript patch: the span).
type Node struct {
	line     int
	lastline int
	SpanBase
}

func (self *Node) Line() int {
	return self.line
}

func (self *Node) SetLine(line int) {
	self.line = line
}

func (self *Node) LastLine() int {
	return self.lastline
}

func (self *Node) SetLastLine(line int) {
	self.lastline = line
}
