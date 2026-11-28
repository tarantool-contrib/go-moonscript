package compiler

// Port of the Lines and Line buffers from moonscript/compile.moon.

import (
	"strconv"
	"strings"
)

const indentChar = "  "

// Lines is a buffer of output lines. Items are string or *Lines.
type Lines struct {
	Items  []any
	posmap map[int]int
}

func NewLines() *Lines { return &Lines{posmap: map[int]int{}} }

func (l *Lines) Len() int { return len(l.Items) }

// MarkPos records pos for line (default: the last line) unless one is set.
func (l *Lines) MarkPos(pos int, line ...int) {
	ln := len(l.Items)
	if len(line) > 0 {
		ln = line[0]
	}
	if pos == 0 {
		return
	}
	if _, ok := l.posmap[ln]; !ok {
		l.posmap[ln] = pos
	}
}

// Add appends a line or lines to the buffer.
func (l *Lines) Add(item any) *Lines {
	switch t := item.(type) {
	case *Line:
		t.Render(l)
	case *Block:
		t.Render(l)
	case nil:
		// table.insert(t, nil) / @[#@ + 1] = nil is a no-op
	default:
		l.Items = append(l.Items, item)
	}
	return l
}

// FlattenPosmap maps output line numbers to source positions.
func (l *Lines) FlattenPosmap(lineNo int, out map[int]int) (map[int]int, int) {
	if out == nil {
		out = map[int]int{}
	}
	for i, item := range l.Items {
		switch t := item.(type) {
		case string:
			lineNo++
			if p, ok := l.posmap[i+1]; ok {
				out[lineNo] = p
			}
			// `line_no += 1 for _ in l\gmatch"\n"` only counts; the
			// assignment after it maps the string's last line
			lineNo += strings.Count(t, "\n")
			if p, ok := l.posmap[i+1]; ok {
				out[lineNo] = p
			}
		case *Lines:
			_, lineNo = t.FlattenPosmap(lineNo, out)
		default:
			panic("Unknown item in Lines")
		}
	}
	return out, lineNo
}

// Flatten renders into buffer; indent "" with hasIndent=false is nil indent.
func (l *Lines) Flatten(indent *string, buffer []string) []string {
	for i, item := range l.Items {
		switch t := item.(type) {
		case string:
			if indent != nil {
				buffer = append(buffer, *indent)
			}
			buffer = append(buffer, t)

			// insert breaks between ambiguous statements
			if i+1 < len(l.Items) {
				if next, ok := l.Items[i+1].(string); ok {
					if !strings.HasSuffix(t, ",") && !strings.HasSuffix(t, "end") && strings.HasPrefix(next, "(") {
						buffer = append(buffer, ";")
					}
				}
			}
			buffer = append(buffer, "\n")
		case *Lines:
			next := indentChar
			if indent != nil {
				next = *indent + indentChar
			}
			buffer = t.Flatten(&next, buffer)
		default:
			panic("Unknown item in Lines")
		}
	}
	return buffer
}

type correlateState struct {
	line    int
	started bool
	posmap  map[int]int
}

// FlattenCorrelated is like Flatten, but pins each line to the line number of
// its marked source position: pads with blank lines when the output is
// behind the source, joins onto the current line when it's ahead.
func (l *Lines) FlattenCorrelated(lineForPos func(int) int, st *correlateState, indent *string, buffer []string) []string {
	for i, item := range l.Items {
		switch t := item.(type) {
		case string:
			target := 0
			if p, ok := l.posmap[i+1]; ok {
				target = lineForPos(p)
			}

			ambiguous := false
			if i > 0 && strings.HasPrefix(t, "(") {
				if prev, ok := l.Items[i-1].(string); ok {
					ambiguous = !strings.HasSuffix(prev, ",") && !strings.HasSuffix(prev, "end")
				}
			}

			if target != 0 && target > st.line {
				if ambiguous {
					buffer = append(buffer, ";")
				}
				buffer = append(buffer, strings.Repeat("\n", target-st.line))
				if indent != nil {
					buffer = append(buffer, *indent)
				}
				st.line = target
			} else if st.started {
				if ambiguous {
					buffer = append(buffer, "; ")
				} else {
					buffer = append(buffer, " ")
				}
			}

			buffer = append(buffer, t)
			st.started = true

			if target != 0 {
				if _, ok := st.posmap[st.line]; !ok {
					st.posmap[st.line] = l.posmap[i+1]
				}
			}
			st.line += strings.Count(t, "\n")
		case *Lines:
			next := indentChar
			if indent != nil {
				next = *indent + indentChar
			}
			buffer = t.FlattenCorrelated(lineForPos, st, &next, buffer)
		default:
			panic("Unknown item in Lines")
		}
	}
	return buffer
}

// Line is a buffer for building up a line: strings and *Block chunks.
type Line struct {
	Chunks []any
	Pos    int
}

func NewLine(items ...any) *Line {
	l := &Line{}
	l.Append(items...)
	return l
}

// Append mirrors Line\append(first, ...): nested lines are merged in, and
// like the Lua vararg recursion, processing stops at the first falsy
// argument after the first one.
func (l *Line) Append(items ...any) {
	for i, item := range items {
		if i > 0 && !truthy(item) {
			return
		}
		switch t := item.(type) {
		case *Line:
			if l.Pos == 0 {
				l.Pos = t.Pos // bubble pos if there isn't one
			}
			l.Chunks = append(l.Chunks, t.Chunks...)
		case nil:
		case int:
			l.Chunks = append(l.Chunks, strconv.Itoa(t))
		default:
			l.Chunks = append(l.Chunks, item)
		}
	}
}

func (l *Line) AppendList(items []any, delim string) {
	for i, item := range items {
		l.Append(item)
		if i < len(items)-1 {
			l.Chunks = append(l.Chunks, delim)
		}
	}
}

// Render writes the line into buffer.
func (l *Line) Render(buffer *Lines) *Lines {
	var current []string
	addCurrent := func() {
		buffer.Add(strings.Join(current, ""))
		buffer.MarkPos(l.Pos)
	}
	for _, chunk := range l.Chunks {
		switch t := chunk.(type) {
		case *Block:
			for _, bc := range t.Render(NewLines()).Items {
				if s, ok := bc.(string); ok {
					current = append(current, s)
				} else {
					addCurrent()
					buffer.Add(bc)
					current = nil
				}
			}
		case string:
			current = append(current, t)
		default:
			panic("unexpected chunk in Line")
		}
	}
	if len(current) > 0 {
		addCurrent()
	}
	return buffer
}

// lineLocator converts a 1-based byte offset in str to a line number.
func lineLocator(str string) func(int) int {
	starts := []int{1}
	for i := 0; i < len(str); i++ {
		if str[i] == '\n' {
			starts = append(starts, i+2)
		}
	}
	return func(pos int) int {
		lo, hi := 1, len(starts)
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if starts[mid-1] <= pos {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo
	}
}
