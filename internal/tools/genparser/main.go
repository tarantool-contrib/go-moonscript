// Command genparser compiles the grammar description (internal/grammar)
// into Go code for package parse (parse/parser_gen.go): one method per
// rule, with every combinator expanded inline into straight-line code that
// runs on the runtime in parse/peg.go.
//
//	go generate ./parse
//
// The generated code keeps the semantics of the combinators exactly (see
// the interpreter in parse/interp_test.go, the differential oracle). It
// relies on the invariant that a failing pattern leaves the parser state
// unchanged, so a failure only has to undo what the enclosing sequence did
// before it, and it uses a static analysis of which patterns can push
// captures or indentation trail entries (or fail at all) to save and
// restore only what can change.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/internal/grammar"
)

func main() {
	out := flag.String("o", "parser_gen.go", "output file")
	flag.Parse()
	src, err := Generate(grammar.Moon())
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		log.Fatal(err)
	}
}

// --- analysis ------------------------------------------------------------------

type facts struct {
	mayCap   bool // can leave capture log entries behind on success
	mayTrail bool // can leave indentation trail entries behind on success
	canFail  bool
}

type analysis struct {
	g     *grammar.Grammar
	rules map[string]facts
}

func analyze(g *grammar.Grammar) *analysis {
	a := &analysis{g: g, rules: map[string]facts{}}
	// mayCap/mayTrail grow from false, canFail shrinks from true: both
	// iterations reach the fixpoint that is safe for code generation
	for name := range g.Rules {
		a.rules[name] = facts{canFail: true}
	}
	for changed := true; changed; {
		changed = false
		for name, n := range g.Rules {
			f := a.node(n)
			if f != a.rules[name] {
				a.rules[name] = f
				changed = true
			}
		}
	}
	return a
}

func (a *analysis) node(n *grammar.Node) facts {
	var kids []facts
	for _, k := range n.Kids {
		kids = append(kids, a.node(k))
	}
	any := func(sel func(facts) bool) bool {
		for _, k := range kids {
			if sel(k) {
				return true
			}
		}
		return false
	}
	all := func(sel func(facts) bool) bool {
		for _, k := range kids {
			if !sel(k) {
				return false
			}
		}
		return true
	}
	capF := func(f facts) bool { return f.mayCap }
	trailF := func(f facts) bool { return f.mayTrail }
	failF := func(f facts) bool { return f.canFail }
	switch n.Op {
	case grammar.OpLit:
		return facts{canFail: n.Str != ""}
	case grammar.OpTrie, grammar.OpAny, grammar.OpSet, grammar.OpCmb:
		return facts{canFail: true}
	case grammar.OpSeq:
		return facts{any(capF), any(trailF), any(failF)}
	case grammar.OpAlt:
		return facts{any(capF), any(trailF), all(failF)}
	case grammar.OpDAlt:
		return facts{any(capF), any(trailF), true}
	case grammar.OpRep:
		return facts{kids[0].mayCap, kids[0].mayTrail, n.N > 0 && kids[0].canFail}
	case grammar.OpOpt:
		return facts{kids[0].mayCap, kids[0].mayTrail, false}
	case grammar.OpNot:
		return facts{canFail: true}
	case grammar.OpLook:
		return facts{canFail: kids[0].canFail}
	case grammar.OpRule:
		return a.rules[n.Name]
	case grammar.OpC, grammar.OpCt, grammar.OpCg, grammar.OpCfn, grammar.OpPos:
		return facts{true, kids[0].mayTrail, kids[0].canFail}
	case grammar.OpCc, grammar.OpCp:
		return facts{mayCap: true}
	case grammar.OpCmt:
		return facts{true, kids[0].mayTrail, true}
	case grammar.OpIndCheck, grammar.OpIndCTopNe:
		return facts{canFail: true}
	case grammar.OpIndAdvance, grammar.OpIndPop:
		return facts{mayTrail: true, canFail: true}
	case grammar.OpIndPush, grammar.OpIndPrevent, grammar.OpIndCPush:
		return facts{mayTrail: true}
	}
	panic(fmt.Sprintf("unknown combinator %d", n.Op))
}

// --- code generation -------------------------------------------------------

type generator struct {
	a *analysis

	// package-level tables
	aux      []string // Go expressions of the aux table entries
	auxIdx   map[string]int
	sets     []string // [256]bool literals
	setIdx   map[[256]bool]int
	tries    []string // newTrie argument lists
	trieIdx  map[string]int
	ruleFunc map[string]string
	static   map[string]int // static depth of the single-slot memoized rules

	// per function
	n    int
	used map[string]bool
}

func (g *generator) auxIndex(expr string) int {
	if i, ok := g.auxIdx[expr]; ok {
		return i
	}
	g.auxIdx[expr] = len(g.aux)
	g.aux = append(g.aux, expr)
	return len(g.aux) - 1
}

func (g *generator) label(prefix string) string {
	g.n++
	return fmt.Sprintf("%s%d", prefix, g.n)
}

func (g *generator) gotoL(l string) string {
	g.used[l] = true
	return "goto " + l + "\n"
}

// emitLabel returns "l:\n" when the label is referenced.
func (g *generator) emitLabel(l string) string {
	if g.used[l] {
		return l + ":\n"
	}
	return ""
}

func byteLit(c byte) string {
	if c >= 0x20 && c < 0x7f || c == '\t' || c == '\n' || c == '\r' {
		return strconv.QuoteRune(rune(c))
	}
	return fmt.Sprintf("0x%02x", c)
}

// setCond returns a Go condition testing whether byte variable v is in set.
func (g *generator) setCond(set *[256]bool, v string) string {
	type run struct{ lo, hi int }
	var runs []run
	for c := 0; c < 256; c++ {
		if !set[c] {
			continue
		}
		if len(runs) > 0 && runs[len(runs)-1].hi == c-1 {
			runs[len(runs)-1].hi = c
		} else {
			runs = append(runs, run{c, c})
		}
	}
	if len(runs) <= 3 {
		var parts []string
		for _, r := range runs {
			switch {
			case r.lo == r.hi:
				parts = append(parts, fmt.Sprintf("%s == %s", v, byteLit(byte(r.lo))))
			case r.hi == r.lo+1:
				parts = append(parts, fmt.Sprintf("%s == %s || %s == %s", v, byteLit(byte(r.lo)), v, byteLit(byte(r.hi))))
			default:
				parts = append(parts, fmt.Sprintf("%s >= %s && %s <= %s", v, byteLit(byte(r.lo)), v, byteLit(byte(r.hi))))
			}
		}
		return strings.Join(parts, " || ")
	}
	i, ok := g.setIdx[*set]
	if !ok {
		var b strings.Builder
		b.WriteString("[256]bool{")
		for c := 0; c < 256; c++ {
			if set[c] {
				fmt.Fprintf(&b, "%s: true, ", byteLit(byte(c)))
			}
		}
		b.WriteString("}")
		i = len(g.sets)
		g.setIdx[*set] = i
		g.sets = append(g.sets, b.String())
	}
	return fmt.Sprintf("set%d[%s]", i, v)
}

// saveVars declares variables holding the parts of the state that can
// change, and returns the declaration and the matching restore code.
func (g *generator) saveVars(caps, trail bool) (decl, restore string) {
	g.n++
	ps := fmt.Sprintf("sp%d", g.n)
	decl = ps + " := p.pos\n"
	restore = "p.pos = " + ps + "\n"
	if caps {
		cs := fmt.Sprintf("sc%d", g.n)
		decl += cs + " := len(p.caps)\n"
		restore += "p.caps = p.caps[:" + cs + "]\n"
	}
	if trail {
		ts := fmt.Sprintf("st%d", g.n)
		decl += ts + " := len(p.trail)\n"
		restore += "if len(p.trail) > " + ts + " { p.rewindTrail(" + ts + ") }\n"
	}
	return decl, restore
}

// gen returns code that matches n: it falls through on success and jumps to
// fail on failure, with the parser state unchanged.
func (g *generator) gen(n *grammar.Node, fail string) string {
	switch n.Op {
	case grammar.OpLit:
		return g.genLit(n.Str, fail)
	case grammar.OpTrie:
		key := fmt.Sprintf("%q", n.Strs)
		i, ok := g.trieIdx[key]
		if !ok {
			var qs []string
			for _, w := range n.Strs {
				qs = append(qs, strconv.Quote(w))
			}
			i = len(g.tries)
			g.trieIdx[key] = i
			g.tries = append(g.tries, strings.Join(qs, ", "))
		}
		return fmt.Sprintf("if !p.matchTrie(trie%d) {\n%s}\n", i, g.gotoL(fail))
	case grammar.OpAny:
		return fmt.Sprintf("if p.pos+%d > len(p.input) {\np.recordFurthest()\n%s}\np.pos += %d\n", n.N, g.gotoL(fail), n.N)
	case grammar.OpSet:
		return fmt.Sprintf("if p.pos >= len(p.input) {\n%s}\nif c := p.input[p.pos]; !(%s) {\n%s}\np.pos++\n",
			g.gotoL(fail), g.setCond(n.Set, "c"), g.gotoL(fail))
	case grammar.OpSeq:
		return g.genSeq(n.Kids, fail)
	case grammar.OpAlt:
		return g.genAlt(n.Kids, fail)
	case grammar.OpDAlt:
		return g.genDAlt(n, fail)
	case grammar.OpRep:
		return g.genRep(n.Kids[0], n.N, fail)
	case grammar.OpOpt:
		l := g.label("opt")
		code := g.gen(n.Kids[0], l)
		return "{\n" + code + g.emitLabel(l) + "}\n"
	case grammar.OpNot:
		kf := g.a.node(n.Kids[0])
		decl, restore := g.saveVars(kf.mayCap, kf.mayTrail)
		l := g.label("not")
		code := g.gen(n.Kids[0], l)
		return "{\n" + decl + code + restore + "p.recordFurthest()\n" + g.gotoL(fail) + g.emitLabel(l) + "}\n"
	case grammar.OpLook:
		kf := g.a.node(n.Kids[0])
		decl, restore := g.saveVars(kf.mayCap, kf.mayTrail)
		return "{\n" + decl + g.gen(n.Kids[0], fail) + restore + "}\n"
	case grammar.OpRule:
		call := "p." + g.ruleFunc[n.Name] + "()"
		canFail := g.a.rules[n.Name].canFail
		if !canFail {
			return call + "\n"
		}
		return "if !" + call + " {\n" + g.gotoL(fail) + "}\n"
	case grammar.OpC:
		kid := n.Kids[0]
		if !g.a.node(kid).mayCap {
			// nothing can be logged inside, so the entry can be pushed
			// once the extent is known
			v := g.label("cs")
			return "{\n" + v + " := p.pos\n" + g.gen(kid, fail) +
				"p.caps = append(p.caps, capEntry{kind: capStr, start: int32(" + v + "), size: int32(p.pos - " + v + ")})\n}\n"
		}
		v := g.label("ci")
		x := g.label("cfail")
		ok := g.label("cok")
		code := g.gen(kid, x)
		s := "{\n" + v + " := len(p.caps)\n" + v + "s := p.pos\n" +
			"p.caps = append(p.caps, capEntry{kind: capStr, start: int32(" + v + "s)})\n" + code +
			"p.caps[" + v + "].size = int32(p.pos - " + v + "s)\n"
		if g.used[x] {
			s += g.gotoL(ok) + x + ":\np.caps = p.caps[:" + v + "]\n" + g.gotoL(fail) + ok + ":\n"
		}
		return s + "}\n"
	case grammar.OpCc:
		var ents []string
		for _, v := range n.Vals {
			if v == nil {
				ents = append(ents, "capEntry{kind: capNil, start: int32(p.pos)}")
				continue
			}
			ents = append(ents, fmt.Sprintf("capEntry{kind: capConst, aux: %d, start: int32(p.pos)}", g.auxIndex(goValue(v))))
		}
		return "p.caps = append(p.caps, " + strings.Join(ents, ", ") + ")\n"
	case grammar.OpCp:
		return "p.caps = append(p.caps, capEntry{kind: capPos, start: int32(p.pos)})\n"
	case grammar.OpCt:
		return g.genBracket("capTblOpen", "capTblClose", 0, n.Kids[0], fail)
	case grammar.OpCg:
		return g.genBracket("capGroupOpen", "capGroupClose", g.auxIndex(strconv.Quote(n.Name)), n.Kids[0], fail)
	case grammar.OpCfn:
		return g.genBracket("capFnOpen", "capFnClose", g.auxIndex("FnCallback("+n.Name+")"), n.Kids[0], fail)
	case grammar.OpPos:
		// materialized like posFn would do with the position capture
		return g.genBracket("capPosOpen", "capPosClose", 0, n.Kids[0], fail)
	case grammar.OpCmb:
		return fmt.Sprintf("if !p.matchBack(%q) {\np.recordFurthest()\n%s}\n", n.Name, g.gotoL(fail))
	case grammar.OpCmt:
		v := g.label("ms")
		return "{\n" + v + " := p.save()\n" + g.gen(n.Kids[0], fail) +
			"if !p.cmt(" + v + ", " + n.Name + ") {\n" + g.gotoL(fail) + "}\n}\n"
	case grammar.OpIndCheck:
		return fmt.Sprintf("if !p.indCheck(%d, %d) {\n%s}\n", n.Stack, n.TabWidth, g.gotoL(fail))
	case grammar.OpIndAdvance:
		return fmt.Sprintf("if !p.indAdvance(%d, %d) {\n%s}\n", n.Stack, n.TabWidth, g.gotoL(fail))
	case grammar.OpIndPush:
		return fmt.Sprintf("p.indPush(%d, %d)\n", n.Stack, n.TabWidth)
	case grammar.OpIndPrevent:
		return fmt.Sprintf("p.stackPush(%d, indPrevent)\n", n.Stack)
	case grammar.OpIndPop:
		return fmt.Sprintf("if !p.indPop(%d) {\n%s}\n", n.Stack, g.gotoL(fail))
	case grammar.OpIndCPush:
		return fmt.Sprintf("p.stackPush(%d, %d)\n", n.Stack, n.N)
	case grammar.OpIndCTopNe:
		return fmt.Sprintf("if !p.indCTopNe(%d, %d) {\n%s}\n", n.Stack, n.N, g.gotoL(fail))
	}
	panic(fmt.Sprintf("unknown combinator %d", n.Op))
}

func goValue(v any) string {
	switch t := v.(type) {
	case string:
		return strconv.Quote(t)
	case int:
		return strconv.Itoa(t)
	case bool:
		return strconv.FormatBool(t)
	}
	panic(fmt.Sprintf("unsupported constant %#v", v))
}

func (g *generator) genLit(s string, fail string) string {
	switch {
	case s == "":
		return ""
	case len(s) == 1:
		// single bytes do not record the furthest failure (as in pgen)
		return fmt.Sprintf("if p.pos >= len(p.input) || p.input[p.pos] != %s {\n%s}\np.pos++\n", byteLit(s[0]), g.gotoL(fail))
	case len(s) <= 4:
		conds := []string{fmt.Sprintf("p.pos+%d > len(p.input)", len(s))}
		for i := 0; i < len(s); i++ {
			conds = append(conds, fmt.Sprintf("p.input[p.pos+%d] != %s", i, byteLit(s[i])))
		}
		return fmt.Sprintf("if %s {\np.recordFurthest()\n%s}\np.pos += %d\n", strings.Join(conds, " || "), g.gotoL(fail), len(s))
	}
	return fmt.Sprintf("if !strings.HasPrefix(p.input[p.pos:], %q) {\np.recordFurthest()\n%s}\np.pos += %d\n", s, g.gotoL(fail), len(s))
}

func (g *generator) genSeq(kids []*grammar.Node, fail string) string {
	restoreL := g.label("seqfail")
	var body strings.Builder
	changed, needCaps, needTrail := false, false, false
	for _, k := range kids {
		// a failure before anything changed needs no restore
		target := fail
		if changed {
			target = restoreL
		}
		body.WriteString(g.gen(k, target))
		kf := g.a.node(k)
		needCaps = needCaps || kf.mayCap
		needTrail = needTrail || kf.mayTrail
		changed = changed || changesState(k)
	}
	if !g.used[restoreL] {
		return body.String()
	}
	decl, restore := g.saveVars(needCaps, needTrail)
	ok := g.label("seqok")
	return "{\n" + decl + body.String() + g.gotoL(ok) + restoreL + ":\n" + restore + g.gotoL(fail) + ok + ":\n}\n"
}

// changesState reports whether n can change the parser state when it
// succeeds.
func changesState(n *grammar.Node) bool {
	switch n.Op {
	case grammar.OpNot, grammar.OpLook, grammar.OpIndCTopNe:
		return false
	case grammar.OpLit:
		return n.Str != ""
	}
	return true
}

func (g *generator) genAlt(kids []*grammar.Node, fail string) string {
	ok := g.label("altok")
	var b strings.Builder
	for i, k := range kids {
		if i == len(kids)-1 {
			b.WriteString(g.gen(k, fail))
			break
		}
		next := g.label("alt")
		code := g.gen(k, next)
		b.WriteString(code)
		if !g.used[next] {
			// this alternative cannot fail: the later ones are unreachable
			break
		}
		b.WriteString(g.gotoL(ok))
		b.WriteString(next + ":\n")
	}
	return "{\n" + b.String() + g.emitLabel(ok) + "}\n"
}

func (g *generator) genDAlt(n *grammar.Node, fail string) string {
	ok := g.label("dok")
	dm := g.label("dm")
	var b strings.Builder
	fmt.Fprintf(&b, "{\n%s := dispatchTables[%d].eof\nif p.pos < len(p.input) {\n%s = dispatchTables[%d].bytes[p.input[p.pos]]\n}\n",
		dm, n.ID-1, dm, n.ID-1)
	for k, kid := range n.Kids {
		bit := uint64(1) << k
		next := g.label("dnext")
		fmt.Fprintf(&b, "if %s.alts&%#x != 0 {\nif %s.s&%#x != 0 {\np.recordFurthest()\n}\n", dm, bit, dm, bit)
		b.WriteString(g.gen(kid, next))
		b.WriteString(g.gotoL(ok))
		b.WriteString("}\n")
		b.WriteString(g.emitLabel(next))
	}
	b.WriteString("p.recordFurthest()\n")
	b.WriteString(g.gotoL(fail))
	b.WriteString(ok + ":\n}\n")
	return b.String()
}

func (g *generator) genRep(kid *grammar.Node, min int, fail string) string {
	kf := g.a.node(kid)
	if kid.Op == grammar.OpSet {
		// a set never records failures nor changes anything but the
		// position, so the loop can be tight
		cond := g.setCond(kid.Set, "c")
		if min == 0 {
			return fmt.Sprintf("for p.pos < len(p.input) {\nif c := p.input[p.pos]; !(%s) {\nbreak\n}\np.pos++\n}\n", cond)
		}
		v := g.label("rn")
		return fmt.Sprintf("{\n%s := 0\nfor p.pos < len(p.input) {\nif c := p.input[p.pos]; !(%s) {\nbreak\n}\np.pos++\n%s++\n}\nif %s < %d {\np.pos -= %s\n%s}\n}\n",
			v, cond, v, v, min, v, g.gotoL(fail))
	}
	done := g.label("repdone")
	ps := g.label("rp")
	code := g.gen(kid, done)
	var b strings.Builder
	b.WriteString("{\n")
	if min == 0 {
		fmt.Fprintf(&b, "for {\n%s := p.pos\n%sif p.pos == %s {\nbreak\n}\n}\n", ps, code, ps)
		b.WriteString(g.emitLabel(done))
		b.WriteString("}\n")
		return b.String()
	}
	decl, restore := g.saveVars(kf.mayCap, kf.mayTrail)
	cnt := g.label("rc")
	fmt.Fprintf(&b, "%s%s := 0\nfor {\n%s := p.pos\n%s%s++\nif p.pos == %s && %s >= %d {\nbreak\n}\n}\n",
		decl, cnt, ps, code, cnt, ps, cnt, min)
	b.WriteString(g.emitLabel(done))
	fmt.Fprintf(&b, "if %s < %d {\n%s%s}\n}\n", cnt, min, restore, g.gotoL(fail))
	return b.String()
}

func (g *generator) genBracket(open, close string, aux int, kid *grammar.Node, fail string) string {
	v := g.label("bi")
	x := g.label("bfail")
	ok := g.label("bok")
	code := g.gen(kid, x)
	s := fmt.Sprintf("{\n%s := p.openCap(%s, %d)\n%sp.closeCap(%s, %s)\n", v, open, aux, code, close, v)
	if g.used[x] {
		s += g.gotoL(ok) + x + ":\np.caps = p.caps[:" + v + "]\n" + g.gotoL(fail) + ok + ":\n"
	}
	return s + "}\n"
}

func (g *generator) genRule(name string, n *grammar.Node) string {
	g.n = 0
	g.used = map[string]bool{}
	slot := g.a.g.MemoSlot(name)
	body := g.gen(n, "fail")
	fn := g.ruleFunc[name]
	var b strings.Builder
	if k := slices.Index(g.a.g.Packrat, name); k >= 0 {
		fmt.Fprintf(&b, "// %s matches rule %s (packrat memoized).\nfunc (p *parser) %s() bool {\n"+
			"if p.pm != nil {\nreturn p.packratCall(%d, (*parser).%sBody)\n}\nreturn p.%sBody()\n}\n\n",
			fn, name, fn, k, fn, fn)
		fn += "Body"
		fmt.Fprintf(&b, "func (p *parser) %s() bool {\n", fn)
	} else {
		fmt.Fprintf(&b, "// %s matches rule %s.\nfunc (p *parser) %s() bool {\n", fn, name, fn)
	}
	if slot >= 0 {
		// a hit does not enter the rule; the parse without the packrat
		// memo might miss instead
		fmt.Fprintf(&b, "if m := &p.memo[%d]; m.pos == p.pos+1 {\n"+
			"if p.pm != nil {\nif d := p.depth + %d; d > p.reach {\np.raiseReachTo(d)\n}\n}\n"+
			"if m.end < 0 {\nreturn false\n}\np.pos = m.end\nreturn true\n}\n", slot, g.static[name])
	}
	b.WriteString("p.depth++\nif p.depth > p.reach {\np.raiseReach()\n}\n")
	if slot >= 0 {
		b.WriteString("start := p.pos\n")
	}
	b.WriteString(body)
	b.WriteString("p.depth--\n")
	if slot >= 0 {
		fmt.Fprintf(&b, "p.memo[%d] = memoSlot{start + 1, p.pos}\n", slot)
	}
	b.WriteString("return true\n")
	if g.used["fail"] {
		b.WriteString("fail:\np.depth--\n")
		if slot >= 0 {
			fmt.Fprintf(&b, "p.memo[%d] = memoSlot{start + 1, -1}\n", slot)
		}
		b.WriteString("return false\n")
	}
	b.WriteString("}\n\n")
	return b.String()
}

// staticDepth returns how deep a call of rule name can nest rule calls
// (1: it calls none). The rule's call graph must be acyclic.
func staticDepth(gr *grammar.Grammar, name string, active map[string]bool) (int, error) {
	if active[name] {
		return 0, fmt.Errorf("rule %s is recursive", name)
	}
	active[name] = true
	defer delete(active, name)
	depth := 1
	var err error
	var walk func(n *grammar.Node)
	walk = func(n *grammar.Node) {
		if n.Op == grammar.OpRule && err == nil {
			var d int
			d, err = staticDepth(gr, n.Name, active)
			depth = max(depth, d+1)
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	walk(gr.Rules[name])
	return depth, err
}

// Generate returns the gofmt-ed source of parser_gen.go.
func Generate(gr *grammar.Grammar) ([]byte, error) {
	g := &generator{
		a:        analyze(gr),
		auxIdx:   map[string]int{},
		setIdx:   map[[256]bool]int{},
		trieIdx:  map[string]int{},
		ruleFunc: map[string]string{},
	}
	names := gr.RuleNames()
	for _, name := range names {
		g.ruleFunc[name] = "r" + name
	}
	if gr.Rules[gr.Start] == nil {
		return nil, fmt.Errorf("undefined start rule %s", gr.Start)
	}
	for _, m := range gr.Memoized {
		if f := g.a.rules[m]; f.mayCap || f.mayTrail {
			return nil, fmt.Errorf("memoized rule %s captures or touches the indentation stacks", m)
		}
	}
	if err := checkRefs(gr); err != nil {
		return nil, err
	}
	g.static = map[string]int{}
	for _, m := range gr.Memoized {
		d, err := staticDepth(gr, m, map[string]bool{})
		if err != nil {
			return nil, fmt.Errorf("memoized rule %s: %w", m, err)
		}
		g.static[m] = d
	}
	if len(gr.Packrat) > 8 {
		return nil, fmt.Errorf("more than 8 packrat rules")
	}
	for _, r := range gr.Packrat {
		if gr.Rules[r] == nil {
			return nil, fmt.Errorf("undefined packrat rule %s", r)
		}
		if gr.MemoSlot(r) >= 0 {
			return nil, fmt.Errorf("packrat rule %s is also single-slot memoized", r)
		}
	}
	var rules strings.Builder
	for _, name := range names {
		rules.WriteString(g.genRule(name, gr.Rules[name]))
	}

	var b bytes.Buffer
	b.WriteString("// Code generated by internal/tools/genparser from internal/grammar; DO NOT EDIT.\n\n")
	b.WriteString("package parse\n\n")
	if strings.Contains(rules.String(), "strings.") {
		b.WriteString("import \"strings\"\n\n")
	}
	// parse.maxMemoSlots
	if len(gr.Memoized) > 16 {
		return nil, fmt.Errorf("more than 16 single-slot memoized rules")
	}
	b.WriteString("// genAux is the aux table (constants, callbacks, group names) the\n// generated code's capture log entries refer to.\nvar genAux = []any{\n")
	for _, e := range g.aux {
		b.WriteString(e + ",\n")
	}
	b.WriteString("}\n\n")
	for i, s := range g.sets {
		fmt.Fprintf(&b, "var set%d = %s\n\n", i, s)
	}
	for i, t := range g.tries {
		fmt.Fprintf(&b, "var trie%d = newTrie(%s)\n\n", i, t)
	}
	b.WriteString("func newGenParser(input string) *parser {\nreturn newParser(input, genAux)\n}\n\n")
	fmt.Fprintf(&b, "// run matches the start rule.\nfunc (p *parser) run() bool {\nreturn p.%s()\n}\n\n", g.ruleFunc[gr.Start])
	b.WriteString(rules.String())
	src, err := format.Source(b.Bytes())
	if err != nil {
		return b.Bytes(), fmt.Errorf("formatting generated code: %w", err)
	}
	return src, nil
}

func checkRefs(gr *grammar.Grammar) error {
	var missing []string
	var walk func(n *grammar.Node)
	walk = func(n *grammar.Node) {
		if n.Op == grammar.OpRule && gr.Rules[n.Name] == nil {
			missing = append(missing, n.Name)
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	for _, n := range gr.Rules {
		walk(n)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("undefined rules: %v", missing)
	}
	return nil
}
