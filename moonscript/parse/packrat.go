package parse

// A packrat memo for a few rules (grammar.Grammar.Packrat) on top of
// pgen's semantics. pgen backtracks without memoizing anything but its
// single-slot memo of position-pure rules, which is exponential on some
// real inputs (nested tables tried as comprehensions first, as in JSON
// embedded in a [[...]] string that is first tried as a comprehension). The
// memo replays the outcome of an earlier evaluation of a rule instead of
// evaluating it again, and is unobservable:
//
//   - the key is the rule, the position and the exact contents of both
//     indentation stacks (interned stack ids)
//   - an evaluation is only stored when it does not depend on the capture
//     log before it (back references, tracked with minScan) and, for a
//     success, when it leaves the stacks as they were (its trail entries,
//     if any, cancel out and need not be replayed) and adds no Cmt values
//     (whose nodes must not be shared); a success replays its capture log
//     segment, which only holds input offsets and aux indices
//   - furthest failures: a replay would only record positions that the
//     stored evaluation already recorded, so it could not raise the
//     furthest position (nor add to its trace)
//   - depth: an entry stores how deep the evaluation could take the parse
//     without the memo (counting every single-slot memo hit as a miss of
//     its static depth), and p.reach accumulates that bound. When it
//     exceeds the limit, the parse is redone without the memo, which then
//     reproduces the reference's depth error (or its absence) exactly.
//
// An evaluation is stored the second time the rule runs at a position and
// replayed from the third time on, which keeps the cost on inputs that
// backtrack little (most of them) near zero.

import "math"

// packratFallback aborts a parse with the packrat memo that might exceed
// the recursion depth limit without it.
type packratFallback struct{}

type packratKey struct {
	rule, pos, s0, s1 int32
}

type packratEntry struct {
	end        int32 // resulting position, -1 for a failure
	rel        int32 // depth bound of the evaluation, relative to its caller
	seg0, seg1 int32 // capture log segment in packrat.segs
}

type packrat struct {
	seen  []uint8 // per position, a bit per packrat rule evaluated there
	table map[packratKey]packratEntry
	segs  []capEntry
	ids   map[[2]int]int32 // stack ids: (id of the stack below, value) -> id
}

func (m *packrat) reset(n int) {
	if cap(m.seen) < n+1 {
		m.seen = make([]uint8, n+1)
	} else {
		m.seen = m.seen[:n+1]
		clear(m.seen)
	}
	if m.table == nil {
		m.table = map[packratKey]packratEntry{}
		m.ids = map[[2]int]int32{}
	} else {
		clear(m.table)
		clear(m.ids)
	}
	m.segs = m.segs[:0]
}

// stackID returns the id of the stack made of the one identified by parent
// (0: empty) with v pushed on top.
func (m *packrat) stackID(parent int32, v int) int32 {
	k := [2]int{int(parent), v}
	if id, ok := m.ids[k]; ok {
		return id
	}
	id := int32(len(m.ids) + 1)
	m.ids[k] = id
	return id
}

// packratEager makes the memo store every evaluation and replay from the
// second one on (tests: it exercises the memo much more).
var packratEager = false

// packratCall runs the packrat rule number rule, whose body (the rule
// without the memo) is body.
func (p *parser) packratCall(rule int, body func(*parser) bool) bool {
	m := p.pm
	bit := uint8(1) << rule
	if m.seen[p.pos]&bit == 0 && !packratEager {
		m.seen[p.pos] |= bit
		return body(p)
	}
	s0, s1 := p.stackIDs()
	key := packratKey{int32(rule), int32(p.pos), s0, s1}
	if e, ok := m.table[key]; ok {
		if d := p.depth + int(e.rel); d > p.reach {
			p.raiseReachTo(d)
		}
		if e.end < 0 {
			return false
		}
		p.caps = append(p.caps, m.segs[e.seg0:e.seg1]...)
		p.pos = int(e.end)
		return true
	}

	d0, c0, v0 := p.depth, len(p.caps), len(p.values)
	reach, scan := p.reach, p.minScan
	p.reach, p.minScan = d0, math.MaxInt
	ok := body(p)
	e := packratEntry{end: -1, rel: int32(p.reach - d0)}
	store := p.minScan >= c0
	p.reach, p.minScan = max(reach, p.reach), min(scan, p.minScan)
	if ok {
		if e0, e1 := p.stackIDs(); !store || e0 != s0 || e1 != s1 || len(p.values) != v0 {
			return true
		}
		e.end = int32(p.pos)
		e.seg0 = int32(len(m.segs))
		m.segs = append(m.segs, p.caps[c0:]...)
		e.seg1 = int32(len(m.segs))
	}
	if store {
		m.table[key] = e
	}
	return ok
}

// runPackrat runs the start rule with the packrat memo, or without it when
// the parse without it might hit the depth limit.
func (p *parser) runPackrat() bool {
	if p.pmBuf == nil {
		p.pmBuf = &packrat{}
	}
	p.reset(p.input, p.aux, p.pmBuf, p.tracing)
	ok, fallback := p.tryRun()
	if fallback {
		p.reset(p.input, p.aux, nil, p.tracing)
		ok = p.run()
	}
	return ok
}

func (p *parser) tryRun() (ok, fallback bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, is := r.(packratFallback); !is {
				panic(r)
			}
			fallback = true
		}
	}()
	return p.run(), false
}
