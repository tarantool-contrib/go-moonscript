package parse

// Runtime of the parser generated from internal/grammar (parser_gen.go). It
// reproduces the semantics of pgen, the parser generator MoonScript v0.8.0
// uses (see moonscript/parse/slow.lua upstream):
//
//   - captures are recorded in a log while matching and only materialized
//     after the whole parse succeeds; backtracking truncates the log
//   - Cmt callbacks materialize their inner captures mid-parse
//   - indentation is tracked with explicit stacks whose pushes and pops are
//     undone through a trail on backtracking
//   - the furthest failure position is recorded at the same kinds of
//     failures as pgen, so parse error locations match the reference
//   - pgen's optimizations that are observable through that position or the
//     recursion depth limit are reproduced: FIRST-byte dispatched choices
//     (DAlt, tables extracted from slow.lua), literal tries and the
//     single-slot memo of position-pure rules (Grammar.Memoized)
//
// On top of these semantics, packrat.go memoizes a few rules in a way that
// cannot be observed, which removes most of pgen's exponential backtracking.
//
// Every pattern leaves the parser state (position, capture log, trail)
// unchanged when it fails; the generated code relies on it.
//
// The reference is the C module (native.c) generated from the same grammar
// as slow.lua; both share these semantics. internal/tools/parsefuzz checks
// the final results against the C module and the step-by-step furthest
// failure bookkeeping against slow.lua; interp_test.go interprets the
// grammar description directly as a differential oracle for the generated
// code.

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
)

//go:generate go run ../../internal/tools/genparser -o parser_gen.go

const maxDepth = 5000

type capKind uint8

const (
	capStr capKind = iota + 1
	capConst
	capNil
	capPos
	capValue
	capTblOpen
	capTblClose
	capGroupOpen
	capGroupClose
	capFnOpen
	capFnClose
	capPosOpen // grammar.Pos: the first inner value, its node position set
	capPosClose
)

// capEntry is one capture log entry. It holds no pointers (constants,
// callbacks and group names live in the parser's aux table), which keeps the
// log cheap to grow and invisible to the garbage collector.
//
// For an open entry size is the distance to its close entry once that is
// pushed, for a close entry the distance back to its open entry, so
// skipping a bracketed capture is O(1).
type capEntry struct {
	kind  capKind
	aux   int32 // aux table index (const, fn, group name) or values index
	start int32 // 0-based input offset
	size  int32
}

type trailEntry struct {
	stack int32
	pop   bool
	val   int
}

// FnCallback is a Cfn transform: receives inner capture values, returns the
// capture value that replaces them (all transforms of the grammar return
// exactly one). It must not retain args.
type FnCallback func(args []any) any

// CmtCallback is a match-time capture. pos is 1-based (position after the
// inner match). Returning ok=false fails the match. newPos is a 1-based
// position, or 0 to keep the current one (Lua's `return true`). It must not
// retain args.
type CmtCallback func(subject string, pos int, args []any) (ok bool, newPos int, vals []any)

type parser struct {
	input    string
	pos      int
	furthest int
	depth    int

	caps   []capEntry
	values []any

	stacks [2][]int
	trail  []trailEntry

	memo [maxMemoSlots]memoSlot
	aux  []any // constants, callbacks and group names of the capture log
	stk  []any // materialization stack

	// table nodes and their items are carved out of these chunks
	nodeSlab []ast.Node
	itemSlab []any

	tracing bool
	trace   []int // furthest-failure increases (0-based), when tracing

	// reach is the maximum recursion depth reached so far; the depth
	// limit is checked whenever it grows. With the packrat memo on it is
	// an upper bound of the depth the parse without it would reach.
	reach int
	pm    *packrat // packrat memo (packrat.go), nil when off
	pmBuf *packrat // kept for reuse
	// sids[k][i] identifies the contents of stacks[k][:i+1] (packrat
	// keys); maintained only when pm is set
	sids    [2][]int32
	minScan int // lowest capture log index a back reference looked at
}

// parserPool recycles parsers with their buffers: the capture log is large
// (zeroing and faulting in fresh memory for it is a sizable part of a
// parse) and holds no pointers.
var parserPool sync.Pool

func newParser(input string, aux []any) *parser {
	p, _ := parserPool.Get().(*parser)
	if p == nil {
		p = &parser{}
	}
	p.reset(input, aux, nil, false)
	return p
}

// reset prepares p for a parse of input, reusing its buffers. pm, when
// not nil, turns the packrat memo on.
func (p *parser) reset(input string, aux []any, pm *packrat, tracing bool) {
	// the log peaks at about 1.5-1.7 entries per input byte on real code;
	// growing it means copying it
	logSize := 2*len(input) + 64
	clear(p.values)
	*p = parser{
		input:    input,
		aux:      aux,
		caps:     p.caps[:0],
		values:   p.values[:0],
		stacks:   [2][]int{append(p.stacks[0][:0], 0), append(p.stacks[1][:0], 1)},
		trail:    p.trail[:0],
		stk:      p.stk[:0],
		nodeSlab: p.nodeSlab,
		itemSlab: p.itemSlab,
		sids:     [2][]int32{p.sids[0][:0], p.sids[1][:0]},
		pm:       pm,
		pmBuf:    p.pmBuf,
		minScan:  math.MaxInt,
		tracing:  tracing,
	}
	if cap(p.caps) < logSize {
		p.caps = make([]capEntry, 0, logSize)
	}
	if pm != nil {
		pm.reset(len(input))
		for k := range p.stacks {
			p.sids[k] = append(p.sids[k], pm.stackID(0, p.stacks[k][0]))
		}
	}
}

// release returns p to the pool. Nothing it references may be used
// afterwards except the materialized values already copied out.
func (p *parser) release() {
	clear(p.values)
	clear(p.stk[:cap(p.stk)])
	p.input = ""
	p.aux = nil
	p.trace = nil
	// the chunks belong to the tree now: the GC scans whole arrays, so
	// keeping their unused rest would keep the tree alive
	p.nodeSlab = nil
	p.itemSlab = nil
	parserPool.Put(p)
}

type state struct {
	pos, caps, trail int
}

func (p *parser) save() state {
	return state{p.pos, len(p.caps), len(p.trail)}
}

func (p *parser) restore(s state) {
	p.pos = s.pos
	p.caps = p.caps[:s.caps]
	if len(p.trail) > s.trail {
		p.rewindTrail(s.trail)
	}
}

func (p *parser) rewindTrail(n int) {
	for len(p.trail) > n {
		t := p.trail[len(p.trail)-1]
		p.trail = p.trail[:len(p.trail)-1]
		s := p.stacks[t.stack]
		if t.pop {
			p.stacks[t.stack] = append(s, t.val)
			if p.pm != nil {
				p.pushID(int(t.stack), t.val)
			}
		} else {
			p.stacks[t.stack] = s[:len(s)-1]
			if p.pm != nil {
				p.sids[t.stack] = p.sids[t.stack][:len(p.sids[t.stack])-1]
			}
		}
	}
}

func (p *parser) recordFurthest() {
	if p.pos > p.furthest {
		p.raiseFurthest()
	}
}

func (p *parser) raiseFurthest() {
	p.furthest = p.pos
	if p.tracing {
		p.trace = append(p.trace, p.pos)
	}
}

func (p *parser) push(kind capKind, aux int32, start, size int) {
	p.caps = append(p.caps, capEntry{kind, aux, int32(start), int32(size)})
}

// openCap pushes the open entry of a bracketed capture and returns its
// index; closeCap pushes the matching close entry.
func (p *parser) openCap(kind capKind, aux int32) int {
	i := len(p.caps)
	p.caps = append(p.caps, capEntry{kind: kind, aux: aux, start: int32(p.pos)})
	return i
}

func (p *parser) closeCap(kind capKind, open int) {
	d := int32(len(p.caps) - open)
	p.caps[open].size = d
	p.caps = append(p.caps, capEntry{kind: kind, start: int32(p.pos), size: d})
}

type dispatchMask struct {
	alts, s uint64
}

// dispatchTable is a FIRST-byte dispatched choice (DAlt): the next input
// byte selects the alternatives that can match there; the others are
// skipped without running. Before trying an alternative that follows a
// skipped one pgen records the furthest failure position (standing in for
// the skipped attempts, the s mask), and when every alternative fails the
// position is recorded as well.
type dispatchTable struct {
	eof   dispatchMask
	bytes [256]dispatchMask
}

// maxMemoSlots bounds the number of single-slot memoized rules.
const maxMemoSlots = 16

type memoSlot struct {
	pos int // memoized start position + 1 (0 = empty)
	end int // resulting position, or -1 for failure
}

type depthError struct{ pos int }

func (e depthError) Error() string {
	return fmt.Sprintf("pgen: max recursion depth (%d) exceeded at position %d", maxDepth, e.pos)
}

// raiseReach is called when a rule entry goes deeper than p.reach.
func (p *parser) raiseReach() {
	p.raiseReachTo(p.depth)
}

func (p *parser) raiseReachTo(d int) {
	p.reach = d
	if d > maxDepth {
		if p.pm != nil {
			// the parse without the packrat memo might hit the limit:
			// only that parse knows where
			panic(packratFallback{})
		}
		panic(depthError{p.pos + 1})
	}
}

// --- literal tries -------------------------------------------------------------

// trieNode is pgen's byte trie for an ordered choice made only of literals.
type trieNode struct {
	next     [256]*trieNode
	leaf     bool // no children
	terminal bool
}

func newTrie(words ...string) *trieNode {
	root := &trieNode{leaf: true}
	for _, w := range words {
		n := root
		for i := 0; i < len(w); i++ {
			c := n.next[w[i]]
			if c == nil {
				c = &trieNode{leaf: true}
				n.next[w[i]] = c
				n.leaf = false
			}
			n = c
		}
		n.terminal = true
	}
	return root
}

// matchTrie takes the longest literal that matches (the same as the ordered
// choice for the word lists used here, where no literal is a prefix of a
// later one) and, when none matches, records the furthest failure at the
// position of the first byte that left the trie rather than at the start.
func (p *parser) matchTrie(root *trieNode) bool {
	start := p.pos
	last := -1
	n := root
	for {
		var c *trieNode
		if p.pos < len(p.input) {
			c = n.next[p.input[p.pos]]
		}
		if c == nil {
			break
		}
		p.pos++
		n = c
		if n.terminal {
			last = p.pos
			if n.leaf {
				return true
			}
		}
	}
	if last >= 0 {
		p.pos = last
		return true
	}
	p.recordFurthest()
	p.pos = start
	return false
}

// --- match-time captures ------------------------------------------------------

// cmt finishes a Cmt whose inner pattern matched from state s: it
// materializes the inner captures, runs fn and replaces the captures by its
// values.
func (p *parser) cmt(s state, fn CmtCallback) bool {
	base := len(p.stk)
	p.evalRange(s.caps, len(p.caps))
	p.caps = p.caps[:s.caps]
	ok, newPos, vals := fn(p.input, p.pos+1, p.stk[base:])
	clear(p.stk[base:])
	p.stk = p.stk[:base]
	if ok && newPos != 0 {
		np := newPos - 1
		if np >= p.pos && np <= len(p.input) {
			p.pos = np
		} else {
			ok = false
		}
	}
	if !ok {
		p.recordFurthest()
		p.restore(s)
		return false
	}
	for _, v := range vals {
		p.values = append(p.values, v)
		p.push(capValue, int32(len(p.values)-1), 0, 0)
	}
	return true
}

// matchBack matches the text of the most recent visible group named name
// (Cmb). It does not record failures; callers do.
func (p *parser) matchBack(name string) bool {
	i := len(p.caps) - 1
	defer func() {
		// the result depends on the log from index i on
		p.minScan = min(p.minScan, i)
	}()
	for i >= 0 {
		k := p.caps[i].kind
		if isClose(k) {
			closeIdx := i
			i -= int(p.caps[i].size)
			if k == capGroupClose && p.aux[p.caps[i].aux] == name {
				var text string
				inner := i + 1
				switch {
				case inner == closeIdx:
					text = p.input[p.caps[i].start:p.caps[closeIdx].start]
				case p.caps[inner].kind == capStr:
					text = p.input[p.caps[inner].start : p.caps[inner].start+p.caps[inner].size]
				case p.caps[inner].kind == capConst:
					s, ok := p.aux[p.caps[inner].aux].(string)
					if !ok {
						return false
					}
					text = s
				default:
					return false
				}
				if strings.HasPrefix(p.input[p.pos:], text) {
					p.pos += len(text)
					return true
				}
				return false
			}
		}
		i--
	}
	return false
}

// --- indentation stacks -----------------------------------------------------

func (p *parser) stackPush(stack, v int) {
	p.stacks[stack] = append(p.stacks[stack], v)
	p.trail = append(p.trail, trailEntry{stack: int32(stack), val: v})
	if p.pm != nil {
		p.pushID(stack, v)
	}
}

func (p *parser) pushID(stack, v int) {
	ids := p.sids[stack]
	parent := int32(0)
	if len(ids) > 0 {
		parent = ids[len(ids)-1]
	}
	p.sids[stack] = append(ids, p.pm.stackID(parent, v))
}

// stackIDs identifies the current contents of both stacks.
func (p *parser) stackIDs() (int32, int32) {
	var a, b int32
	if s := p.sids[0]; len(s) > 0 {
		a = s[len(s)-1]
	}
	if s := p.sids[1]; len(s) > 0 {
		b = s[len(s)-1]
	}
	return a, b
}

func (p *parser) stackPop(stack int) bool {
	s := p.stacks[stack]
	if len(s) == 0 {
		return false
	}
	v := s[len(s)-1]
	p.stacks[stack] = s[:len(s)-1]
	p.trail = append(p.trail, trailEntry{stack: int32(stack), pop: true, val: v})
	if p.pm != nil {
		p.sids[stack] = p.sids[stack][:len(p.sids[stack])-1]
	}
	return true
}

func (p *parser) top(stack int) (int, bool) {
	s := p.stacks[stack]
	if len(s) == 0 {
		return 0, false
	}
	return s[len(s)-1], true
}

const indPrevent = math.MaxInt

func (p *parser) measure(tabWidth int) (int, int) {
	w := 0
	i := p.pos
	for i < len(p.input) {
		switch p.input[i] {
		case ' ':
			w++
		case '\t':
			w += tabWidth
		default:
			return w, i
		}
		i++
	}
	return w, i
}

// indCheck consumes indentation whose width must equal the top of the
// stack.
func (p *parser) indCheck(stack, tabWidth int) bool {
	w, end := p.measure(tabWidth)
	if t, ok := p.top(stack); ok && t == w {
		p.pos = end
		return true
	}
	p.recordFurthest()
	return false
}

// indAdvance pushes the indentation width if deeper than the top, consuming
// nothing.
func (p *parser) indAdvance(stack, tabWidth int) bool {
	w, _ := p.measure(tabWidth)
	if t, ok := p.top(stack); ok && w > t {
		p.stackPush(stack, w)
		return true
	}
	p.recordFurthest()
	return false
}

// indPush consumes indentation and pushes its width.
func (p *parser) indPush(stack, tabWidth int) {
	w, end := p.measure(tabWidth)
	p.stackPush(stack, w)
	p.pos = end
}

// indPop pops the stack.
func (p *parser) indPop(stack int) bool {
	if p.stackPop(stack) {
		return true
	}
	p.recordFurthest()
	return false
}

// indCTopNe succeeds when the top differs from v.
func (p *parser) indCTopNe(stack, v int) bool {
	if t, ok := p.top(stack); ok && t != v {
		return true
	}
	p.recordFurthest()
	return false
}

// --- capture materialization ----------------------------------------------
//
// Values are evaluated onto p.stk, a stack shared by all nesting levels: a
// bracket evaluates its inner captures on top of it, then replaces them by
// its own values. Callbacks get a window of the stack as args, which they
// must not retain.

func isOpen(k capKind) bool {
	return k == capTblOpen || k == capGroupOpen || k == capFnOpen || k == capPosOpen
}

func isClose(k capKind) bool {
	return k == capTblClose || k == capGroupClose || k == capFnClose || k == capPosClose
}

// capSkip returns the index after the capture at i.
func (p *parser) capSkip(i int) int {
	if isOpen(p.caps[i].kind) {
		return i + int(p.caps[i].size) + 1
	}
	return i + 1
}

// evalRange pushes the values of the captures in [i, end), skipping named
// groups (which only back references and named table fields see).
func (p *parser) evalRange(i, end int) {
	for i < end {
		if p.caps[i].kind == capGroupOpen {
			i = p.capSkip(i)
		} else {
			i = p.capEval(i)
		}
	}
}

// capEvalGroup pushes the first value of the group at i, or its whole match
// when it has none.
func (p *parser) capEvalGroup(i int) int {
	open := i
	after := p.capSkip(i)
	closeIdx := after - 1
	before := len(p.stk)
	for j := open + 1; j < closeIdx; {
		j = p.capEval(j)
		if len(p.stk) > before {
			p.stk = p.stk[:before+1]
			return after
		}
	}
	p.stk = append(p.stk, p.input[p.caps[open].start:p.caps[closeIdx].start])
	return after
}

// capEval pushes the values of the capture at i and returns the index after
// it.
func (p *parser) capEval(i int) int {
	c := p.caps[i]
	switch c.kind {
	case capStr:
		p.stk = append(p.stk, p.input[c.start:c.start+c.size])
		return i + 1
	case capConst:
		p.stk = append(p.stk, p.aux[c.aux])
		return i + 1
	case capNil:
		p.stk = append(p.stk, nil)
		return i + 1
	case capPos:
		p.stk = append(p.stk, int(c.start)+1)
		return i + 1
	case capValue:
		p.stk = append(p.stk, p.values[c.aux])
		return i + 1
	case capGroupOpen:
		return p.capEvalGroup(i)
	case capFnOpen:
		fn := p.aux[c.aux].(FnCallback)
		end := i + int(c.size)
		base := len(p.stk)
		p.evalRange(i+1, end)
		if len(p.stk) == base {
			p.stk = append(p.stk, p.input[c.start:p.caps[end].start])
		}
		v := fn(p.stk[base:])
		clear(p.stk[base+1:])
		p.stk = append(p.stk[:base], v)
		return end + 1
	case capPosOpen:
		// posFn without boxing the position nor allocating its args
		end := i + int(c.size)
		base := len(p.stk)
		p.evalRange(i+1, end)
		var v any
		if len(p.stk) > base {
			v = p.stk[base]
			clear(p.stk[base:])
			p.stk = p.stk[:base]
		}
		if n, ok := v.(*ast.Node); ok {
			n.Pos = int(c.start) + 1
		}
		p.stk = append(p.stk, v)
		return end + 1
	default: // capTblOpen
		end := i + int(c.size)
		base := len(p.stk)
		for j := i + 1; j < end; {
			if p.caps[j].kind == capGroupOpen {
				// named fields are not used by the MoonScript grammar
				j = p.capEvalGroup(j)
				p.stk = p.stk[:len(p.stk)-1]
				continue
			}
			j = p.capEval(j)
		}
		tbl := p.newNode()
		if n := len(p.stk) - base; n > 0 {
			tbl.Items = p.newItems(n)
			copy(tbl.Items, p.stk[base:])
			clear(p.stk[base:])
			p.stk = p.stk[:base]
		}
		p.stk = append(p.stk, tbl)
		return end + 1
	}
}

func (p *parser) newNode() *ast.Node {
	if len(p.nodeSlab) == 0 {
		p.nodeSlab = make([]ast.Node, 256)
	}
	n := &p.nodeSlab[0]
	p.nodeSlab = p.nodeSlab[1:]
	return n
}

// newItems returns a slice of length and capacity n (appending to it
// reallocates).
func (p *parser) newItems(n int) []any {
	if n > 64 {
		return make([]any, n)
	}
	if len(p.itemSlab) < n {
		p.itemSlab = make([]any, 1024)
	}
	s := p.itemSlab[:n:n]
	p.itemSlab = p.itemSlab[n:]
	return s
}

// materialize evaluates the whole capture log after a successful parse.
func (p *parser) materialize() []any {
	p.evalRange(0, len(p.caps))
	return p.stk
}
