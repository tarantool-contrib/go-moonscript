// Lua pattern match functions for Go
package pm

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	EOS      = -1
	_UNKNOWN = -2
	// go-moonscript patch: maxRecursionLevel is how deep the matcher can
	// recurse. It recurses once per capture boundary and once per repeated
	// or optional item it is trying, so the depth depends on the pattern
	// only, not on the subject (it recursed once per repeated byte: a
	// greedy repeat over more than a million bytes failed with
	// "pattern/input too complex", which Lua 5.1 matches). A level takes a
	// few hundred bytes of Go stack. Lua 5.1 has no limit (deep enough
	// patterns overflow its C stack). After mdogan/gopher-lua 3f80512213.
	maxRecursionLevel = 200000
	// go-moonscript patch: maxCaptures is Lua 5.1's LUA_MAXCAPTURES ("too
	// many captures"); it also bounds how deep parsePattern and
	// compilePattern recurse, once per nested capture.
	maxCaptures = 32
)

/* Error {{{ */

type Error struct {
	Pos     int
	Message string
}

func newError(pos int, message string, args ...interface{}) *Error {
	if len(args) == 0 {
		return &Error{pos, message}
	}
	return &Error{pos, fmt.Sprintf(message, args...)}
}

func (e *Error) Error() string {
	switch e.Pos {
	case EOS:
		return fmt.Sprintf("%s at EOS", e.Message)
	case _UNKNOWN:
		return fmt.Sprintf("%s", e.Message)
	default:
		return fmt.Sprintf("%s at %d", e.Message, e.Pos)
	}
}

/* }}} */

/* MatchData {{{ */

type MatchData struct {
	// captured positions
	// layout
	// xxxx xxxx xxxx xxx0 : caputured positions
	// xxxx xxxx xxxx xxx1 : position captured positions
	captures []uint32
}

func newMatchState() *MatchData { return &MatchData{[]uint32{}} }

func (st *MatchData) addPosCapture(s, pos int) {
	for s+1 >= len(st.captures) {
		st.captures = append(st.captures, 0)
	}
	st.captures[s] = (uint32(pos) << 1) | 1
	st.captures[s+1] = (uint32(pos) << 1) | 1
}

func (st *MatchData) setCapture(s, pos int) uint32 {
	for s >= len(st.captures) {
		st.captures = append(st.captures, 0)
	}
	v := st.captures[s]
	st.captures[s] = (uint32(pos) << 1)
	return v
}

func (st *MatchData) restoreCapture(s int, pos uint32) { st.captures[s] = pos }

func (st *MatchData) CaptureLength() int { return len(st.captures) }

func (st *MatchData) IsPosCapture(idx int) bool { return (st.captures[idx] & 1) == 1 }

func (st *MatchData) Capture(idx int) int { return int(st.captures[idx] >> 1) }

// go-moonscript patch: capUnfinished is the end of a capture whose ')' the
// pattern lacks (an odd end after an even start; a position capture has
// both odd): the pattern ends inside the capture, which matches as in Lua
// 5.1 with the capture left open (CAP_UNFINISHED).
const capUnfinished = 1

// IsUnfinished reports whether the capture at idx (its first slot, as for
// Capture) is unfinished: an unclosed '(' runs to the end of the pattern.
// Lua 5.1 raises "unfinished capture" when the value of such a capture is
// asked for. go-moonscript patch.
func (st *MatchData) IsUnfinished(idx int) bool {
	return st.captures[idx]&1 == 0 && idx+1 < len(st.captures) && st.captures[idx+1]&1 == 1
}

/* }}} */

/* scanner {{{ */

type scannerState struct {
	Pos     int
	started bool
}

type scanner struct {
	src      []byte
	State    scannerState
	saved    scannerState
	captures int // go-moonscript patch: captures parsed so far
	// go-moonscript patch: the error of a malformed item, which ends the
	// pattern (see errorPattern)
	malformed int
}

func newScanner(src []byte) *scanner {
	return &scanner{
		src: src,
		State: scannerState{
			Pos:     0,
			started: false,
		},
		saved: scannerState{},
	}
}

func (sc *scanner) Length() int { return len(sc.src) }

func (sc *scanner) Next() int {
	if !sc.State.started {
		sc.State.started = true
		if len(sc.src) == 0 {
			sc.State.Pos = EOS
		}
	} else {
		sc.State.Pos = sc.NextPos()
	}
	if sc.State.Pos == EOS {
		return EOS
	}
	return int(sc.src[sc.State.Pos])
}

func (sc *scanner) CurrentPos() int {
	return sc.State.Pos
}

func (sc *scanner) NextPos() int {
	if sc.State.Pos == EOS || sc.State.Pos >= len(sc.src)-1 {
		return EOS
	}
	if !sc.State.started {
		return 0
	} else {
		return sc.State.Pos + 1
	}
}

func (sc *scanner) Peek() int {
	cureof := sc.State.Pos == EOS
	ch := sc.Next()
	if !cureof {
		if sc.State.Pos == EOS {
			sc.State.Pos = len(sc.src) - 1
		} else {
			sc.State.Pos--
			if sc.State.Pos < 0 {
				sc.State.Pos = 0
				sc.State.started = false
			}
		}
	}
	return ch
}

func (sc *scanner) Save() { sc.saved = sc.State }

func (sc *scanner) Restore() { sc.State = sc.saved }

/* }}} */

/* bytecode {{{ */

type opCode int

const (
	opChar opCode = iota
	opMatch
	opTailMatch
	opJmp
	opSplit
	opSave
	opPSave
	opBrace
	opNumber
	// go-moonscript patch: opRepeat matches Class repeated, then the rest of
	// the pattern. Operand1 is the fewest repeats (0 for '*' and '-', 1 for
	// '+'); Operand2 is 1 when it is lazy ('-'), trying the fewest repeats
	// first.
	opRepeat
	// go-moonscript patch: opFrontier is %f[set] (Lua 5.1.5): matches the
	// empty string between a byte not in Class and one in it, '\0' before
	// the subject and after it.
	opFrontier
	// go-moonscript patch: opError raises patternErrors[Operand1].
	opError
	// go-moonscript patch: opUnfinished ends capture slot Operand1 (the
	// second slot of a capture) as unfinished, then matches the rest.
	opUnfinished
)

// go-moonscript patch: the errors of malformed items, which Lua 5.1 raises
// when the matcher reaches them (not when the pattern is parsed: "x%" does
// not match "abc" and raises no error).
var patternErrors = [...]string{
	"",
	"malformed pattern (ends with '%')",
	"malformed pattern (missing ']')",
	"unbalanced pattern",
	"missing '[' after '%f' in pattern",
	"invalid pattern capture",
}

const (
	errEndsWithPercent = 1 + iota
	errMissingBracket
	errUnbalanced
	errFrontierBracket
	errInvalidPatternCapture
)

type inst struct {
	OpCode   opCode
	Class    class
	Operand1 int
	Operand2 int
}

/* }}} */

/* classes {{{ */

type class interface {
	Matches(ch int) bool
}

type dotClass struct{}

func (pn *dotClass) Matches(ch int) bool { return true }

type charClass struct {
	Ch int
}

func (pn *charClass) Matches(ch int) bool { return pn.Ch == ch }

type singleClass struct {
	Class int
}

func (pn *singleClass) Matches(ch int) bool {
	ret := false
	switch pn.Class {
	case 'a', 'A':
		ret = 'A' <= ch && ch <= 'Z' || 'a' <= ch && ch <= 'z'
	case 'c', 'C':
		ret = (0x00 <= ch && ch <= 0x1F) || ch == 0x7F
	case 'd', 'D':
		ret = '0' <= ch && ch <= '9'
	case 'l', 'L':
		ret = 'a' <= ch && ch <= 'z'
	case 'p', 'P':
		ret = (0x21 <= ch && ch <= 0x2f) || (0x3a <= ch && ch <= 0x40) || (0x5b <= ch && ch <= 0x60) || (0x7b <= ch && ch <= 0x7e)
	case 's', 'S':
		switch ch {
		case ' ', '\f', '\n', '\r', '\t', '\v':
			ret = true
		}
	case 'u', 'U':
		ret = 'A' <= ch && ch <= 'Z'
	case 'w', 'W':
		ret = '0' <= ch && ch <= '9' || 'A' <= ch && ch <= 'Z' || 'a' <= ch && ch <= 'z'
	case 'x', 'X':
		ret = '0' <= ch && ch <= '9' || 'a' <= ch && ch <= 'f' || 'A' <= ch && ch <= 'F'
	case 'z', 'Z':
		ret = ch == 0
	default:
		return ch == pn.Class
	}
	if 'A' <= pn.Class && pn.Class <= 'Z' {
		return !ret
	}
	return ret
}

type setClass struct {
	IsNot   bool
	Classes []class
}

func (pn *setClass) Matches(ch int) bool {
	for _, class := range pn.Classes {
		if class.Matches(ch) {
			return !pn.IsNot
		}
	}
	return pn.IsNot
}

type rangeClass struct {
	Begin class
	End   class
}

func (pn *rangeClass) Matches(ch int) bool {
	switch begin := pn.Begin.(type) {
	case *charClass:
		end, ok := pn.End.(*charClass)
		if !ok {
			return false
		}
		return begin.Ch <= ch && ch <= end.Ch
	}
	return false
}

// }}}

// patterns {{{

type pattern interface{}

type singlePattern struct {
	Class class
}

type seqPattern struct {
	MustHead bool
	MustTail bool
	Patterns []pattern
}

type repeatPattern struct {
	Type  int
	Class class
}

type posCapPattern struct{}

type capPattern struct {
	Pattern pattern
	// go-moonscript patch: the pattern ends before the capture's ')', so the
	// capture stays open, as Lua 5.1's start_capture leaves it at the end of
	// the pattern
	Unfinished bool
}

type numberPattern struct {
	N int
}

type bracePattern struct {
	Begin int
	End   int
}

// go-moonscript patch: frontierPattern is %f[set].
type frontierPattern struct {
	Set class
}

// go-moonscript patch: errorPattern is a malformed item, the last one of
// the pattern: it raises patternErrors[Error] when the matcher reaches it.
type errorPattern struct {
	Error int
}

// }}}

/* parse {{{ */

func parseClass(sc *scanner, allowset bool) class {
	ch := sc.Next()
	switch ch {
	case '%':
		return &singleClass{sc.Next()}
	case '.':
		if allowset {
			return &dotClass{}
		}
		return &charClass{ch}
	case '[':
		if allowset {
			return parseClassSet(sc)
		}
		return &charClass{ch}
	//case '^' '$', '(', ')', ']', '*', '+', '-', '?':
	//	panic(newError(sc.CurrentPos(), "invalid %c", ch))
	case EOS:
		panic(newError(sc.CurrentPos(), "unexpected EOS"))
	default:
		return &charClass{ch}
	}
}

// parseClassSet parses the set whose '[' the scanner is at, as Lua 5.1's
// classEnd and matchbracketclass read it. go-moonscript patch: a '%x' item
// was taken as the start of a range ("[%a-z]" matched nothing) and a range
// end as the start of another ("[a-c-e]"). A set without its ']' sets
// sc.malformed and returns nil.
func parseClassSet(sc *scanner) class {
	src := sc.src
	p := sc.CurrentPos() + 1
	set := &setClass{false, []class{}}
	if p < len(src) && src[p] == '^' {
		set.IsNot = true
		p++
	}
	// the first byte is in the set, even if it is ']'
	ec := p
	for {
		if ec >= len(src) {
			sc.malformed = errMissingBracket
			sc.State.Pos = len(src) - 1
			return nil
		}
		ec++
		if src[ec-1] == '%' && ec < len(src) {
			ec++
		}
		if ec < len(src) && src[ec] == ']' {
			break
		}
	}
	for ; p < ec; p++ {
		switch {
		case src[p] == '%':
			p++
			set.Classes = append(set.Classes, &singleClass{int(src[p])})
		case src[p+1] == '-' && p+2 < ec:
			set.Classes = append(set.Classes, &rangeClass{&charClass{int(src[p])}, &charClass{int(src[p+2])}})
			p += 2
		default:
			set.Classes = append(set.Classes, &charClass{int(src[p])})
		}
	}
	sc.State.Pos = ec
	return set
}

func parsePattern(sc *scanner, toplevel bool) *seqPattern {
	pat := &seqPattern{}
	if toplevel {
		if sc.Peek() == '^' {
			sc.Next()
			pat.MustHead = true
		}
	}
	for {
		ch := sc.Peek()
		switch ch {
		case '%':
			sc.Save()
			sc.Next()
			switch sc.Peek() {
			case EOS: // go-moonscript patch: an error when reached, as in Lua 5.1
				sc.malformed = errEndsWithPercent
			case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				// go-moonscript patch: %0 is an invalid capture index when
				// reached, as in Lua 5.1 (it was an error in any case)
				pat.Patterns = append(pat.Patterns, &numberPattern{sc.Next() - 48})
			case 'b':
				sc.Next()
				begin, end := sc.Next(), sc.Next()
				if end == EOS { // go-moonscript patch: missing delimiters
					sc.malformed = errUnbalanced
					break
				}
				pat.Patterns = append(pat.Patterns, &bracePattern{begin, end})
			case 'f': // go-moonscript patch: frontier, as in Lua 5.1.5
				sc.Next()
				if sc.Peek() != '[' {
					sc.malformed = errFrontierBracket
					break
				}
				sc.Next()
				if set := parseClassSet(sc); set != nil {
					pat.Patterns = append(pat.Patterns, &frontierPattern{set})
				}
			default:
				sc.Restore()
				pat.Patterns = append(pat.Patterns, &singlePattern{parseClass(sc, true)})
			}
			if sc.malformed != 0 {
				pat.Patterns = append(pat.Patterns, &errorPattern{sc.malformed})
				return pat
			}
		case '.', '[', ']':
			class := parseClass(sc, true)
			if sc.malformed != 0 { // go-moonscript patch
				pat.Patterns = append(pat.Patterns, &errorPattern{sc.malformed})
				return pat
			}
			pat.Patterns = append(pat.Patterns, &singlePattern{class})
		//case ']':
		//	panic(newError(sc.CurrentPos(), "invalid ']'"))
		case ')':
			if toplevel {
				// go-moonscript patch: an error when reached, as in Lua 5.1
				sc.Next()
				sc.malformed = errInvalidPatternCapture
				pat.Patterns = append(pat.Patterns, &errorPattern{sc.malformed})
				return pat
			}
			return pat
		case '(':
			sc.captures++ // go-moonscript patch
			if sc.captures > maxCaptures {
				panic(newError(_UNKNOWN, "too many captures"))
			}
			sc.Next()
			if sc.Peek() == ')' {
				sc.Next()
				pat.Patterns = append(pat.Patterns, &posCapPattern{})
			} else {
				ret := &capPattern{Pattern: parsePattern(sc, false)}
				if sc.malformed != 0 { // go-moonscript patch: the pattern ends there
					pat.Patterns = append(pat.Patterns, ret)
					return pat
				}
				if sc.Peek() != ')' {
					// go-moonscript patch: the end of the pattern, not an
					// error (it was "unfinished capture at EOS"): Lua 5.1's
					// do_match matches at the end of the pattern whatever
					// captures are open, and a '$' there is the anchor
					ret.Unfinished = true
					pat.Patterns = append(pat.Patterns, ret)
					pat.MustTail = ret.Pattern.(*seqPattern).MustTail
					goto exit
				}
				sc.Next()
				pat.Patterns = append(pat.Patterns, ret)
			}
		case '*', '+', '-', '?':
			sc.Next()
			if len(pat.Patterns) > 0 {
				spat, ok := pat.Patterns[len(pat.Patterns)-1].(*singlePattern)
				if ok {
					pat.Patterns = pat.Patterns[0 : len(pat.Patterns)-1]
					pat.Patterns = append(pat.Patterns, &repeatPattern{ch, spat.Class})
					continue
				}
			}
			pat.Patterns = append(pat.Patterns, &singlePattern{&charClass{ch}})
		case '$':
			// go-moonscript patch: the anchor at the end of the pattern, also
			// inside a capture (which then is unfinished)
			if sc.NextPos() == sc.Length()-1 || sc.NextPos() == EOS {
				pat.MustTail = true
			} else {
				pat.Patterns = append(pat.Patterns, &singlePattern{&charClass{ch}})
			}
			sc.Next()
		case EOS:
			sc.Next()
			goto exit
		default:
			sc.Next()
			pat.Patterns = append(pat.Patterns, &singlePattern{&charClass{ch}})
		}
	}
exit:
	return pat
}

type iptr struct {
	insts   []inst
	capture int
	kinds   map[int]captureKind // go-moonscript patch: the captures started so far, by first slot
}

// go-moonscript patch: what is known of a capture where a back reference
// (%1-%9) to it is compiled. A pattern has no alternatives and no repeated
// groups, so whether capture N is started, closed or a position capture at
// %N is known when the pattern is compiled. From mdogan/gopher-lua
// 3f80512213.
type captureKind int

const (
	captureOpen captureKind = iota
	captureClosed
	capturePosition
)

// What an opNumber instruction does, in its Operand2.
const (
	backrefMatch    = iota // matches the text of the capture
	backrefInvalid         // "invalid capture index": not started or not closed yet
	backrefPosition        // never matches, as in Lua 5.1 (CAP_POSITION is longer than any subject)
)

func compilePattern(p pattern, ps ...*iptr) []inst {
	var ptr *iptr
	toplevel := false
	if len(ps) == 0 {
		toplevel = true
		ptr = &iptr{[]inst{inst{opSave, nil, 0, -1}}, 2, map[int]captureKind{}}
	} else {
		ptr = ps[0]
	}
	switch pat := p.(type) {
	case *singlePattern:
		ptr.insts = append(ptr.insts, inst{opChar, pat.Class, -1, -1})
	case *seqPattern:
		for _, cp := range pat.Patterns {
			compilePattern(cp, ptr)
		}
	case *repeatPattern:
		idx := len(ptr.insts)
		switch pat.Type {
		case '*': // go-moonscript patch: opRepeat
			ptr.insts = append(ptr.insts, inst{opRepeat, pat.Class, 0, 0})
		case '+':
			ptr.insts = append(ptr.insts, inst{opRepeat, pat.Class, 1, 0})
		case '-':
			ptr.insts = append(ptr.insts, inst{opRepeat, pat.Class, 0, 1})
		case '?':
			ptr.insts = append(ptr.insts,
				inst{opSplit, nil, idx + 1, idx + 2},
				inst{opChar, pat.Class, -1, -1})
		}
	case *posCapPattern:
		ptr.kinds[ptr.capture] = capturePosition // go-moonscript patch
		ptr.insts = append(ptr.insts, inst{opPSave, nil, ptr.capture, -1})
		ptr.capture += 2
	case *capPattern:
		c0, c1 := ptr.capture, ptr.capture+1
		ptr.capture += 2
		ptr.kinds[c0] = captureOpen // go-moonscript patch
		ptr.insts = append(ptr.insts, inst{opSave, nil, c0, -1})
		compilePattern(pat.Pattern, ptr)
		if pat.Unfinished { // go-moonscript patch: stays open (a %N to it is invalid)
			ptr.insts = append(ptr.insts, inst{opUnfinished, nil, c1, -1})
			break
		}
		ptr.insts = append(ptr.insts, inst{opSave, nil, c1, -1})
		ptr.kinds[c0] = captureClosed
	case *bracePattern:
		ptr.insts = append(ptr.insts, inst{opBrace, nil, pat.Begin, pat.End})
	case *frontierPattern: // go-moonscript patch
		ptr.insts = append(ptr.insts, inst{opFrontier, pat.Set, -1, -1})
	case *errorPattern: // go-moonscript patch
		ptr.insts = append(ptr.insts, inst{opError, nil, pat.Error, -1})
	case *numberPattern:
		backref := backrefInvalid // go-moonscript patch
		switch kind, ok := ptr.kinds[pat.N*2]; {
		case ok && kind == captureClosed:
			backref = backrefMatch
		case ok && kind == capturePosition:
			backref = backrefPosition
		}
		ptr.insts = append(ptr.insts, inst{opNumber, nil, pat.N, backref})
	}
	if toplevel {
		if p.(*seqPattern).MustTail {
			ptr.insts = append(ptr.insts, inst{opSave, nil, 1, -1}, inst{opTailMatch, nil, -1, -1})
		}
		ptr.insts = append(ptr.insts, inst{opSave, nil, 1, -1}, inst{opMatch, nil, -1, -1})
	}
	return ptr.insts
}

/* }}} parse */

/* VM {{{ */

// go-moonscript patch: matching stops when the context of the LState is
// done, as the VM does between instructions: a backtracking pattern can run
// for years inside one string.find (yuin/gopher-lua#559, #521). From
// mdogan/gopher-lua 64abd4bac7 and yuin/gopher-lua#566 (draarivpatel-ui).

// ctxCheckInterval is how many steps (VM instructions, bytes scanned by a
// repeat, %b or a back reference) run between two context checks.
const ctxCheckInterval = 1024

// ctxDone is the panic that stops matching when the context is done.
type ctxDone struct{ err error }

type matcher struct {
	ctx   context.Context // nil: never stops
	steps int
}

func (vm *matcher) step() {
	if vm.ctx == nil {
		return
	}
	vm.steps++
	if vm.steps >= ctxCheckInterval {
		vm.checkContext()
	}
}

//go:noinline
func (vm *matcher) checkContext() {
	vm.steps = 0
	if err := vm.ctx.Err(); err != nil {
		panic(ctxDone{err})
	}
}

func (vm *matcher) matches(c class, ch byte) bool {
	vm.step()
	return c.Matches(int(ch))
}

// Simple recursive virtual machine based on the
// "Regular Expression Matching: the Virtual Machine Approach" (https://swtch.com/~rsc/regexp/regexp2.html)
//
// go-moonscript patch: a method of matcher, which stops it when its context
// is done.
func (vm *matcher) recursiveVM(src []byte, insts []inst, pc, sp, recLevel int, ms ...*MatchData) (bool, int, *MatchData) {
	recLevel++
	if recLevel > maxRecursionLevel {
		panic(newError(_UNKNOWN, "pattern/input too complex"))
	}
	var m *MatchData
	if len(ms) == 0 {
		m = newMatchState()
	} else {
		m = ms[0]
	}
redo:
	vm.step()
	inst := insts[pc]
	switch inst.OpCode {
	case opChar:
		if sp >= len(src) || !inst.Class.Matches(int(src[sp])) {
			return false, sp, m
		}
		pc++
		sp++
		goto redo
	case opRepeat:
		// go-moonscript patch: the repeated item is one character class, so
		// the repeats are tried in a loop, as Lua 5.1 does (max_expand and
		// min_expand), not by recursing once per repeated byte; only the
		// rest of the pattern is matched recursively, once per try. From
		// mdogan/gopher-lua 3f80512213.
		minimum := sp + inst.Operand1
		if inst.Operand2 == 0 {
			// greedy: the most repeats, then the rest of the pattern after
			// each count, from the most to the fewest
			end := sp
			for end < len(src) && vm.matches(inst.Class, src[end]) {
				end++
			}
			for ; end > minimum; end-- {
				if ok, nsp, _ := vm.recursiveVM(src, insts, pc+1, end, recLevel, m); ok {
					return true, nsp, m
				}
			}
			if end < minimum {
				return false, sp, m
			}
			sp = end
			pc++
			goto redo
		}
		// lazy: the rest of the pattern after the fewest repeats, then
		// after one more repeat at a time
		for ; sp < minimum; sp++ {
			if sp >= len(src) || !vm.matches(inst.Class, src[sp]) {
				return false, sp, m
			}
		}
		for {
			if ok, nsp, _ := vm.recursiveVM(src, insts, pc+1, sp, recLevel, m); ok {
				return true, nsp, m
			}
			if sp >= len(src) || !vm.matches(inst.Class, src[sp]) {
				return false, sp, m
			}
			sp++
		}
	case opMatch:
		return true, sp, m
	case opTailMatch:
		return sp >= len(src), sp, m
	case opJmp:
		pc = inst.Operand1
		goto redo
	case opSplit:
		if ok, nsp, _ := vm.recursiveVM(src, insts, inst.Operand1, sp, recLevel, m); ok {
			return true, nsp, m
		}
		pc = inst.Operand2
		goto redo
	case opSave:
		s := m.setCapture(inst.Operand1, sp)
		if ok, nsp, _ := vm.recursiveVM(src, insts, pc+1, sp, recLevel, m); ok {
			return true, nsp, m
		}
		m.restoreCapture(inst.Operand1, s)
		return false, sp, m
	case opUnfinished: // go-moonscript patch
		s := m.setCapture(inst.Operand1, 0)
		m.captures[inst.Operand1] = capUnfinished
		if ok, nsp, _ := vm.recursiveVM(src, insts, pc+1, sp, recLevel, m); ok {
			return true, nsp, m
		}
		m.restoreCapture(inst.Operand1, s)
		return false, sp, m
	case opPSave:
		m.addPosCapture(inst.Operand1, sp+1)
		pc++
		goto redo
	case opFrontier: // go-moonscript patch
		prev, cur := 0, 0
		if sp > 0 {
			prev = int(src[sp-1])
		}
		if sp < len(src) {
			cur = int(src[sp])
		}
		if inst.Class.Matches(prev) || !inst.Class.Matches(cur) {
			return false, sp, m
		}
		pc++
		goto redo
	case opError: // go-moonscript patch
		panic(newError(_UNKNOWN, "%s", patternErrors[inst.Operand1]))
	case opBrace:
		if sp >= len(src) || int(src[sp]) != inst.Operand1 {
			return false, sp, m
		}
		count := 1
		for sp = sp + 1; sp < len(src); sp++ {
			vm.step()
			if int(src[sp]) == inst.Operand2 {
				count--
			}
			if count == 0 {
				pc++
				sp++
				goto redo
			}
			if int(src[sp]) == inst.Operand1 {
				count++
			}
		}
		return false, sp, m
	case opNumber:
		switch inst.Operand2 { // go-moonscript patch: see backrefMatch
		case backrefInvalid:
			panic(newError(_UNKNOWN, "invalid capture index"))
		case backrefPosition:
			return false, sp, m
		}
		idx := inst.Operand1 * 2
		capture := src[m.Capture(idx):m.Capture(idx+1)]
		for i := 0; i < len(capture); i++ {
			vm.step()
			if i+sp >= len(src) || capture[i] != src[i+sp] {
				return false, sp, m
			}
		}
		pc++
		sp += len(capture)
		goto redo
	}
	panic("should not reach here")
}

/* }}} */

/* API {{{ */

func Find(p string, src []byte, offset, limit int) (matches []*MatchData, err error) {
	return FindContext(nil, p, src, offset, limit)
}

// FindContext is Find, but stops when ctx is done and returns ctx.Err(); ctx
// may be nil. go-moonscript patch.
func FindContext(ctx context.Context, p string, src []byte, offset, limit int) (matches []*MatchData, err error) {
	defer func() {
		if v := recover(); v != nil {
			switch perr := v.(type) {
			case *Error:
				err = perr
			case ctxDone:
				matches, err = nil, perr.err
			default:
				panic(v)
			}
		}
	}()
	cp := compiledPattern(p)
	if cp.literal {
		// go-moonscript patch: linear, so it needs no context checks
		return findLiteral(cp.lit, src, offset, limit), nil
	}
	return findVM(&matcher{ctx: ctx}, cp, src, offset, limit), nil
}

// findVM runs the pattern VM from each position from offset on.
func findVM(vm *matcher, cp *compiled, src []byte, offset, limit int) (matches []*MatchData) {
	matches = []*MatchData{}
	var m *MatchData
	for sp := offset; sp <= len(src); {
		// go-moonscript patch: the match data of a failed attempt is
		// reused for the next one (from PR #558)
		if m == nil {
			m = &MatchData{make([]uint32, 0, cp.ncap)}
		} else {
			m.captures = m.captures[:0]
		}
		ok, nsp, ms := vm.recursiveVM(src, cp.insts, 0, sp, 0, m)
		sp++
		if ok {
			if sp < nsp {
				sp = nsp
			}
			matches = append(matches, ms)
			m = nil
		}
		if len(matches) == limit || cp.mustHead {
			break
		}
	}
	return
}

// go-moonscript patch: compiled patterns are cached (from PR #558 by
// pixel2175, as a package-level cache).

type compiled struct {
	insts    []inst
	mustHead bool
	ncap     int    // the number of capture slots
	literal  bool   // the pattern has no special characters
	lit      []byte // the pattern, if literal
}

// findLiteral is Find for a pattern without special characters, which
// matches where its bytes are (idea from taybart/midnight 9adf4c4196). Its
// results are exactly findVM's, also for the limits other than 1 and -1.
func findLiteral(lit []byte, src []byte, offset, limit int) []*MatchData {
	matches := []*MatchData{}
	if offset < 0 || offset > len(src) || limit == 0 && !bytes.HasPrefix(src[offset:], lit) {
		// (findVM stops after a first attempt that fails when limit is 0)
		return matches
	}
	for sp := offset; sp <= len(src); {
		i := bytes.Index(src[sp:], lit)
		if i < 0 {
			break
		}
		start := sp + i
		sp = start + len(lit)
		matches = append(matches, &MatchData{[]uint32{uint32(start) << 1, uint32(sp) << 1}})
		if len(matches) == limit {
			break
		}
	}
	return matches
}

const (
	maxCachedPatterns   = 1024
	maxCachedPatternLen = 256
)

var (
	patternCache sync.Map // pattern string -> *compiled
	nCached      int32
)

// compiledPattern compiles p, or returns it from the cache. Like
// parsePattern, it panics with an *Error for an invalid pattern. A compiled
// pattern is never modified, so it can be shared by goroutines.
func compiledPattern(p string) *compiled {
	if v, ok := patternCache.Load(p); ok {
		return v.(*compiled)
	}
	pat := parsePattern(newScanner([]byte(p)), true)
	cp := &compiled{insts: compilePattern(pat), mustHead: pat.MustHead}
	if len(p) > 0 && !strings.ContainsAny(p, "^$*+?.()[]%-") {
		cp.literal, cp.lit = true, []byte(p)
	}
	for _, in := range cp.insts {
		switch in.OpCode {
		case opSave, opUnfinished:
			cp.ncap = max(cp.ncap, in.Operand1+1)
		case opPSave:
			cp.ncap = max(cp.ncap, in.Operand1+2)
		}
	}
	if len(p) <= maxCachedPatternLen && atomic.LoadInt32(&nCached) < maxCachedPatterns {
		if _, loaded := patternCache.LoadOrStore(p, cp); !loaded {
			atomic.AddInt32(&nCached, 1)
		}
	}
	return cp
}

/* }}} */
