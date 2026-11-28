package flow_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// gen writes random Lua programs that exercise the control flow and the
// data flow: locals with and without values, assignments of one and more
// names (also the same one twice), fields, circular assignments, closures
// reading and writing upvalues, parameters, self, loops with constant and
// other conditions, break, return, and under LuaJIT goto and labels. They
// feed the comparison with luacheck and the fuzz target's seeds.
type gen struct {
	r      *rand.Rand
	b      strings.Builder
	indent int
	jit    bool
	// the locals visible, innermost last
	vars   []string
	nvar   int
	depth  int
	loops  int // enclosing loops of the current function
	vararg bool
	labels [][]string // the labels of the enclosing blocks of the function
	// the labels each enclosing block puts at its end, for gotos forward
	pending [][]string
	nlabel  int
	budget  int
}

func genProgram(seed uint64, jit bool) string {
	g := &gen{r: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), jit: jit, budget: 40 + int(seed%60)}
	g.vararg = true
	g.block(true)
	return g.b.String()
}

func (g *gen) line(format string, args ...any) {
	g.b.WriteString(strings.Repeat("  ", g.indent))
	fmt.Fprintf(&g.b, format, args...)
	g.b.WriteByte('\n')
}

func (g *gen) chance(n int) bool { return g.r.IntN(100) < n }

func (g *gen) newVar() string {
	g.nvar++
	// few names, so that they shadow and are reused
	names := []string{"a", "b", "c", "d", "e", "x", "y", "t"}
	return names[g.r.IntN(len(names))]
}

func (g *gen) someVar() string {
	if len(g.vars) == 0 || g.chance(10) {
		return []string{"G", "H"}[g.r.IntN(2)]
	}
	return g.vars[len(g.vars)-1-g.r.IntN(min(len(g.vars), 6))]
}

func (g *gen) cond() string {
	if g.chance(25) {
		return []string{"true", "false", "nil", "1", `"s"`, "{}", "(true)", "not x"}[g.r.IntN(8)]
	}
	return g.expr(1)
}

func (g *gen) expr(d int) string {
	if d > 2 {
		return g.atom()
	}
	switch g.r.IntN(12) {
	case 0:
		return g.expr(d+1) + " + " + g.expr(d+1)
	case 1:
		return g.expr(d+1) + " and " + g.expr(d+1)
	case 2:
		return g.expr(d+1) + " or " + g.expr(d+1)
	case 3:
		return "f(" + g.expr(d+1) + ")"
	case 4:
		if g.depth < 3 {
			return g.function("", false)
		}
	case 5:
		return "{" + g.expr(d+1) + ", k = " + g.expr(d+1) + "}"
	case 6:
		return g.someVar() + ".k"
	case 7:
		return "(" + g.expr(d+1) + ")"
	case 8:
		if g.vararg {
			return "..."
		}
	}
	return g.atom()
}

func (g *gen) atom() string {
	switch g.r.IntN(6) {
	case 0:
		return fmt.Sprint(g.r.IntN(10))
	case 1:
		return "nil"
	}
	return g.someVar()
}

// function writes a function expression ("function (...) ... end") or,
// with name, the rest of a function statement.
func (g *gen) function(name string, method bool) string {
	saved := g.b
	g.b = strings.Builder{}
	var params []string
	for i := g.r.IntN(3); i > 0; i-- {
		params = append(params, g.newVar())
	}
	va := g.chance(30)
	plist := strings.Join(params, ", ")
	if va {
		if plist != "" {
			plist += ", "
		}
		plist += "..."
	}
	head := "function(" + plist + ")"
	if name != "" {
		head = "function " + name + "(" + plist + ")"
	}
	nv, loops, vararg, labels, pending := len(g.vars), g.loops, g.vararg, g.labels, g.pending
	if method {
		g.vars = append(g.vars, "self")
	}
	g.vars = append(g.vars, params...)
	g.loops, g.vararg, g.labels, g.pending = 0, va, nil, nil
	g.depth++
	g.b.WriteString(head + "\n")
	g.indent++
	g.block(false)
	g.indent--
	g.b.WriteString(strings.Repeat("  ", g.indent) + "end")
	g.depth--
	g.vars, g.loops, g.vararg, g.labels, g.pending = g.vars[:nv], loops, vararg, labels, pending
	body := g.b.String()
	g.b = saved
	return body
}

// block writes the statements of a block; the last may be a return or a
// break.
func (g *gen) block(top bool) {
	nv := len(g.vars)
	g.labels = append(g.labels, nil)
	g.pending = append(g.pending, nil)
	n := g.r.IntN(5)
	if top {
		n = 8 + g.r.IntN(8)
	}
	for i := 0; i < n && g.budget > 0; i++ {
		g.budget--
		g.stmt()
	}
	switch {
	case len(g.pending[len(g.pending)-1]) > 0: // the labels come last
	case g.chance(15):
		var vals []string
		for i := g.r.IntN(3); i > 0; i-- {
			vals = append(vals, g.expr(1))
		}
		g.line("return %s", strings.Join(vals, ", "))
	case g.loops > 0 && g.chance(15):
		g.line("break")
	case g.jit && g.chance(10):
		g.gotoStmt()
	}
	for _, l := range g.pending[len(g.pending)-1] {
		g.line("::%s::", l)
	}
	g.labels = g.labels[:len(g.labels)-1]
	g.pending = g.pending[:len(g.pending)-1]
	g.vars = g.vars[:nv]
}

func (g *gen) gotoStmt() {
	var all []string
	for _, ls := range g.labels {
		all = append(all, ls...)
	}
	if len(all) > 0 && g.chance(70) {
		g.line("goto %s", all[g.r.IntN(len(all))])
		return
	}
	// a label at the end of this block, which the block adds
	g.nlabel++
	l := fmt.Sprintf("e%d", g.nlabel)
	g.pending[len(g.pending)-1] = append(g.pending[len(g.pending)-1], l)
	g.line("goto %s", l)
}

func (g *gen) nested(head string, body func()) {
	g.line("%s", head)
	g.indent++
	body()
	g.indent--
}

func (g *gen) stmt() {
	if g.depth > 3 {
		g.line("%s = %s", g.someVar(), g.atom())
		return
	}
	switch g.r.IntN(22) {
	case 0, 1, 2:
		name := g.newVar()
		switch g.r.IntN(4) {
		case 0:
			g.line("local %s", name)
		case 1:
			g.line("local %s = %s", name, g.expr(0))
		case 2:
			other := g.newVar()
			g.line("local %s, %s = %s", name, other, g.expr(0))
			g.vars = append(g.vars, other)
		case 3:
			g.line("local %s, %s = f()", name, name)
		}
		g.vars = append(g.vars, name)
	case 3:
		name := g.newVar()
		g.vars = append(g.vars, name)
		g.line("local %s", g.function(name, false))
	case 4, 5, 6:
		g.line("%s = %s", g.someVar(), g.expr(0))
	case 7:
		a, b := g.someVar(), g.someVar()
		g.line("%s, %s = %s, %s", a, b, g.expr(1), g.expr(1))
	case 8:
		v := g.someVar()
		switch g.r.IntN(3) {
		case 0:
			g.line("%s.k = %s.j", v, v)
		case 1:
			g.line("%s[%s] = %s", v, g.atom(), g.expr(1))
		case 2:
			g.line("%s.k = f(%s)", v, v)
		}
	case 9:
		g.line("%s(%s)", []string{"print", g.someVar()}[g.r.IntN(2)], g.expr(1))
	case 10, 11:
		g.nested("if "+g.cond()+" then", func() { g.block(false) })
		for g.chance(30) {
			g.nested("elseif "+g.cond()+" then", func() { g.block(false) })
		}
		if g.chance(40) {
			g.nested("else", func() { g.block(false) })
		}
		g.line("end")
	case 12:
		g.loops++
		g.nested("while "+g.cond()+" do", func() { g.block(false) })
		g.loops--
		g.line("end")
	case 13:
		g.loops++
		g.nested("repeat", func() { g.block(false) })
		g.loops--
		g.line("until %s", g.cond())
	case 14:
		v := g.newVar()
		head := fmt.Sprintf("for %s = %s, %s do", v, g.atom(), g.atom())
		g.loops++
		g.line("%s", head)
		g.indent++
		g.vars = append(g.vars, v)
		g.block(false)
		g.vars = g.vars[:len(g.vars)-1]
		g.indent--
		g.loops--
		g.line("end")
	case 15:
		k, v := g.newVar(), g.newVar()
		g.loops++
		g.line("for %s, %s in pairs(%s) do", k, v, g.someVar())
		g.indent++
		g.vars = append(g.vars, k, v)
		g.block(false)
		g.vars = g.vars[:len(g.vars)-2]
		g.indent--
		g.loops--
		g.line("end")
	case 16:
		g.nested("do", func() { g.block(false) })
		g.line("end")
	case 17:
		switch g.r.IntN(3) {
		case 0:
			g.line("%s", g.function(g.someVar(), false))
		case 1:
			g.line("%s", g.function(g.someVar()+".f", false))
		case 2:
			g.line("%s", g.function(g.someVar()+":m", true))
		}
	case 18:
		g.line("f(%s)", g.function("", false))
	case 19, 20:
		if g.jit {
			g.nlabel++
			l := fmt.Sprintf("l%d", g.nlabel)
			g.labels[len(g.labels)-1] = append(g.labels[len(g.labels)-1], l)
			g.line("::%s::", l)
			if g.chance(50) {
				g.gotoStmt()
			}
			return
		}
		g.line("%s = %s", g.someVar(), g.expr(0))
	case 21:
		v := g.someVar()
		g.line("%s = %s + 1", v, v)
	}
}
