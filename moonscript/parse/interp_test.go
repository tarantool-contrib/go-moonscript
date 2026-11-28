package parse

// A direct interpreter of the grammar description (internal/grammar): every
// combinator becomes a closure. It was the parser before parser_gen.go and
// is kept as a differential oracle for the generated code (see
// differential_test.go): both run on the same parser runtime (peg.go), so
// trees, error positions, furthest-failure traces and depth-limit errors
// must agree exactly.

import (
	"fmt"
	"strings"
	"sync"

	"github.com/tarantool-contrib/go-moonscript/internal/grammar"
)

// interpFns maps the callback names of the grammar description to their
// implementations.
var interpFns = map[string]any{
	"posFn":               FnCallback(posFn),
	"statementFn":         FnCallback(statementFn),
	"assignTransformFn":   FnCallback(assignTransformFn),
	"singleAssignFn":      FnCallback(singleAssignFn),
	"expFn":               FnCallback(expFn),
	"joinChainFn":         FnCallback(joinChainFn),
	"luaStringFn":         FnCallback(luaStringFn),
	"keyValueShorthandFn": FnCallback(keyValueShorthandFn),
	"assignableCmt":       CmtCallback(assignableCmt),
}

type pattern struct {
	m func(p *parser) bool
}

type interpRule struct {
	name string
	pat  *pattern
	memo int // memo slot index, -1 when the rule is not memoized
}

type interpGrammar struct {
	start    *interpRule
	rules    map[string]*interpRule
	aux      []any
	auxNames map[string]int32
}

var (
	interpOnce sync.Once
	interp     *interpGrammar
)

func interpreter() *interpGrammar {
	interpOnce.Do(func() { interp = newInterp(grammar.Moon()) })
	return interp
}

func newInterp(g *grammar.Grammar) *interpGrammar {
	ig := &interpGrammar{rules: map[string]*interpRule{}, auxNames: map[string]int32{}}
	for name := range g.Rules {
		ig.rules[name] = &interpRule{name: name, memo: g.MemoSlot(name)}
	}
	for name, n := range g.Rules {
		ig.rules[name].pat = ig.compile(n)
	}
	if len(g.Memoized) > maxMemoSlots {
		panic("parse: too many memoized rules")
	}
	ig.start = ig.rules[g.Start]
	return ig
}

func (ig *interpGrammar) auxIndex(v any) int32 {
	if s, ok := v.(string); ok {
		if i, ok := ig.auxNames[s]; ok {
			return i
		}
		ig.auxNames[s] = int32(len(ig.aux))
	}
	ig.aux = append(ig.aux, v)
	return int32(len(ig.aux) - 1)
}

func (ig *interpGrammar) compile(n *grammar.Node) *pattern {
	kids := make([]*pattern, len(n.Kids))
	for i, k := range n.Kids {
		kids[i] = ig.compile(k)
	}
	switch n.Op {
	case grammar.OpLit:
		return iLit(n.Str)
	case grammar.OpTrie:
		return iTrie(n.Strs...)
	case grammar.OpAny:
		return iAny(n.N)
	case grammar.OpSet:
		return iSet(n.Set)
	case grammar.OpSeq:
		return iSeq(kids...)
	case grammar.OpAlt:
		return iAlt(kids...)
	case grammar.OpDAlt:
		return iDAlt(n.ID, kids...)
	case grammar.OpRep:
		return iRep(kids[0], n.N)
	case grammar.OpOpt:
		return iOpt(kids[0])
	case grammar.OpNot:
		return iNot(kids[0])
	case grammar.OpLook:
		return iLook(kids[0])
	case grammar.OpRule:
		r := ig.rules[n.Name]
		if r == nil {
			panic("parse: undefined rule " + n.Name)
		}
		return iV(r)
	case grammar.OpC:
		return iC(kids[0])
	case grammar.OpCc:
		idx := make([]int32, len(n.Vals))
		for i, v := range n.Vals {
			idx[i] = -1
			if v != nil {
				idx[i] = ig.auxIndex(v)
			}
		}
		return iCc(idx)
	case grammar.OpCp:
		return iCp()
	case grammar.OpCt:
		return iBracket(capTblOpen, capTblClose, 0, kids[0])
	case grammar.OpCg:
		return iBracket(capGroupOpen, capGroupClose, ig.auxIndex(n.Name), kids[0])
	case grammar.OpCfn:
		return iBracket(capFnOpen, capFnClose, ig.auxIndex(interpFns[n.Name].(FnCallback)), kids[0])
	case grammar.OpPos:
		// literally Cfn(Seq(Cp(), patt), "posFn")
		return iBracket(capFnOpen, capFnClose, ig.auxIndex(FnCallback(posFn)), iSeq(iCp(), kids[0]))
	case grammar.OpCmb:
		return iCmb(n.Name)
	case grammar.OpCmt:
		return iCmt(kids[0], interpFns[n.Name].(CmtCallback))
	case grammar.OpIndCheck:
		return &pattern{m: func(p *parser) bool { return p.indCheck(n.Stack, n.TabWidth) }}
	case grammar.OpIndAdvance:
		return &pattern{m: func(p *parser) bool { return p.indAdvance(n.Stack, n.TabWidth) }}
	case grammar.OpIndPush:
		return &pattern{m: func(p *parser) bool { p.indPush(n.Stack, n.TabWidth); return true }}
	case grammar.OpIndPrevent:
		return &pattern{m: func(p *parser) bool { p.stackPush(n.Stack, indPrevent); return true }}
	case grammar.OpIndPop:
		return &pattern{m: func(p *parser) bool { return p.indPop(n.Stack) }}
	case grammar.OpIndCPush:
		return &pattern{m: func(p *parser) bool { p.stackPush(n.Stack, n.N); return true }}
	case grammar.OpIndCTopNe:
		return &pattern{m: func(p *parser) bool { return p.indCTopNe(n.Stack, n.N) }}
	}
	panic(fmt.Sprintf("parse: unknown combinator %d", n.Op))
}

func (ig *interpGrammar) newParser(input string) *parser {
	return newParser(input, ig.aux)
}

// run matches the start rule (entered like any rule reference).
func (ig *interpGrammar) run(p *parser) bool {
	p.depth++
	ok := ig.start.pat.m(p)
	p.depth--
	return ok
}

func (ig *interpGrammar) match(input string) (vals []any, ok bool, failPos int) {
	p := ig.newParser(input)
	if !ig.run(p) {
		return nil, false, p.furthest + 1
	}
	return p.materialize(), true, 0
}

func (ig *interpGrammar) trace(input string) (trace []int) {
	p := ig.newParser(input)
	p.tracing = true
	defer func() {
		_ = recover() // a Cmt callback error ends the parse, as in FurthestTrace
		trace = p.trace
	}()
	ig.run(p)
	return p.trace
}

// --- combinators ------------------------------------------------------------

func iLit(s string) *pattern {
	if len(s) == 1 {
		c := s[0]
		return &pattern{m: func(p *parser) bool {
			if p.pos < len(p.input) && p.input[p.pos] == c {
				p.pos++
				return true
			}
			return false
		}}
	}
	if s == "" {
		return &pattern{m: func(p *parser) bool { return true }}
	}
	return &pattern{m: func(p *parser) bool {
		if strings.HasPrefix(p.input[p.pos:], s) {
			p.pos += len(s)
			return true
		}
		p.recordFurthest()
		return false
	}}
}

func iTrie(words ...string) *pattern {
	root := newTrie(words...)
	return &pattern{m: func(p *parser) bool { return p.matchTrie(root) }}
}

func iAny(n int) *pattern {
	return &pattern{m: func(p *parser) bool {
		if p.pos+n <= len(p.input) {
			p.pos += n
			return true
		}
		p.recordFurthest()
		return false
	}}
}

func iSet(set *[256]bool) *pattern {
	return &pattern{m: func(p *parser) bool {
		if p.pos < len(p.input) && set[p.input[p.pos]] {
			p.pos++
			return true
		}
		return false
	}}
}

func iSeq(ps ...*pattern) *pattern {
	return &pattern{m: func(p *parser) bool {
		s := p.save()
		for _, sub := range ps {
			if !sub.m(p) {
				p.restore(s)
				return false
			}
		}
		return true
	}}
}

func iAlt(ps ...*pattern) *pattern {
	return &pattern{m: func(p *parser) bool {
		s := p.save()
		for _, sub := range ps {
			if sub.m(p) {
				return true
			}
			p.restore(s)
		}
		return false
	}}
}

func iDAlt(id int, ps ...*pattern) *pattern {
	tbl := &dispatchTables[id-1]
	for _, m := range tbl.bytes {
		if m.alts>>len(ps) != 0 {
			panic(fmt.Sprintf("parse: dispatch table %d has more alternatives than %d", id, len(ps)))
		}
	}
	return &pattern{m: func(p *parser) bool {
		dm := tbl.eof
		if p.pos < len(p.input) {
			dm = tbl.bytes[p.input[p.pos]]
		}
		s := p.save()
		for k, sub := range ps {
			bit := uint64(1) << k
			if dm.alts&bit == 0 {
				continue
			}
			if dm.s&bit != 0 {
				p.recordFurthest()
			}
			if sub.m(p) {
				return true
			}
			p.restore(s)
		}
		p.recordFurthest()
		return false
	}}
}

func iRep(sub *pattern, min int) *pattern {
	return &pattern{m: func(p *parser) bool {
		start := p.save()
		count := 0
		for {
			s := p.save()
			if !sub.m(p) {
				p.restore(s)
				break
			}
			count++
			if p.pos == s.pos && count >= min {
				// empty match: stop looping (lpeg would reject the pattern)
				break
			}
		}
		if count < min {
			p.restore(start)
			return false
		}
		return true
	}}
}

func iOpt(sub *pattern) *pattern {
	return &pattern{m: func(p *parser) bool {
		s := p.save()
		if !sub.m(p) {
			p.restore(s)
		}
		return true
	}}
}

func iNot(sub *pattern) *pattern {
	return &pattern{m: func(p *parser) bool {
		s := p.save()
		if sub.m(p) {
			p.restore(s)
			p.recordFurthest()
			return false
		}
		p.restore(s)
		return true
	}}
}

func iLook(sub *pattern) *pattern {
	return &pattern{m: func(p *parser) bool {
		s := p.save()
		ok := sub.m(p)
		p.restore(s)
		return ok
	}}
}

func iV(r *interpRule) *pattern {
	return &pattern{m: func(p *parser) bool {
		if r.memo >= 0 {
			// pgen's single-slot memo for position-pure rules: a hit returns
			// the stored outcome without entering the rule (so it neither
			// counts towards the recursion depth nor records failures)
			slot := &p.memo[r.memo]
			if slot.pos == p.pos+1 {
				if slot.end < 0 {
					return false
				}
				p.pos = slot.end
				return true
			}
		}
		p.depth++
		if p.depth > maxDepth {
			panic(depthError{p.pos + 1})
		}
		start := p.pos
		ok := r.pat.m(p)
		if r.memo >= 0 {
			slot := &p.memo[r.memo]
			slot.pos = start + 1
			slot.end = -1
			if ok {
				slot.end = p.pos
			}
		}
		p.depth--
		return ok
	}}
}

func iC(sub *pattern) *pattern {
	return &pattern{m: func(p *parser) bool {
		idx := len(p.caps)
		start := p.pos
		p.push(capStr, 0, start, 0)
		if !sub.m(p) {
			p.caps = p.caps[:idx]
			return false
		}
		p.caps[idx].size = int32(p.pos - start)
		return true
	}}
}

func iCc(idx []int32) *pattern {
	return &pattern{m: func(p *parser) bool {
		for _, i := range idx {
			if i < 0 {
				p.push(capNil, 0, p.pos, 0)
			} else {
				p.push(capConst, i, p.pos, 0)
			}
		}
		return true
	}}
}

func iCp() *pattern {
	return &pattern{m: func(p *parser) bool {
		p.push(capPos, 0, p.pos, 0)
		return true
	}}
}

func iBracket(open, close capKind, aux int32, sub *pattern) *pattern {
	return &pattern{m: func(p *parser) bool {
		idx := p.openCap(open, aux)
		if !sub.m(p) {
			p.caps = p.caps[:idx]
			return false
		}
		p.closeCap(close, idx)
		return true
	}}
}

func iCmb(name string) *pattern {
	return &pattern{m: func(p *parser) bool {
		if p.matchBack(name) {
			return true
		}
		p.recordFurthest()
		return false
	}}
}

func iCmt(sub *pattern, fn CmtCallback) *pattern {
	return &pattern{m: func(p *parser) bool {
		s := p.save()
		if !sub.m(p) {
			return false
		}
		return p.cmt(s, fn)
	}}
}
