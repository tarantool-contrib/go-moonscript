package flow_test

import (
	"fmt"
	"sort"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/flow"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
)

// warning is a finding of a lint pass, with luacheck's code.
type warning struct {
	pos  ast.Pos
	code string
	msg  string
}

func (w warning) String() string { return fmt.Sprintf("%s: W%s %s", w.pos, w.code, w.msg) }

// luacheckLint is a lint pass over a Program with luacheck's rules for the
// codes the package has queries for: unreachable code (511), loops
// executed at most once (512), empty blocks (541, 542), unused values (311,
// 312, 313), uninitialized variables (321, 341), at luacheck's positions.
// With parens, a parenthesized function at the end of an assignment's
// values is no function, as for luacheck (flow.LuacheckOptions).
func luacheckLint(chunk *ast.Chunk, info *scope.Info, p *flow.Program, parens bool) []warning {
	src := chunk.Lines.Source()
	var out []warning
	add := func(offset int, code, format string, args ...any) {
		out = append(out, warning{chunk.Pos(offset), code, fmt.Sprintf(format, args...)})
	}
	for _, g := range p.Graphs {
		for _, n := range g.Unreachable() {
			if n.Kind == flow.LoopEnd {
				add(n.Stmt.Span().Start, "512", "loop is executed at most once")
			} else {
				add(nodeStart(n), "511", "unreachable code")
			}
		}
	}
	for _, e := range p.Empty {
		switch e.Kind {
		case flow.EmptyDo:
			add(e.Stmt.Span().Start, "541", "empty do..end block")
		case flow.EmptyThen: // at the "then"
			add(nextToken(src, e.Stmt.(*ast.IfStmt).Condition.OuterSpan().End), "542", "empty if branch")
		case flow.EmptyElse: // at the "else"
			add(nextToken(src, e.Stmt.(*ast.IfStmt).ThenSpan.End), "542", "empty if branch")
		}
	}
	for _, v := range info.Vars {
		// luacheck knows no implicit arg: it is a global; "_" is a
		// placeholder, whose values don't matter
		if v.Kind == scope.KindLabel || v.Kind == scope.KindArg || v.Name == "_" {
			continue
		}
		defs := p.VarDefs(v)
		accessed, mutated := false, false
		for _, u := range p.VarUses(v) {
			if u.Kind == flow.Read {
				accessed = true
			} else {
				mutated = true
			}
		}
		onlyEmpty := len(defs) == 1 && defs[0].Empty // "never set" (221), not 321
		for _, u := range p.VarUses(v) {
			if !onlyEmpty && u.Uninitialized() {
				if u.Kind == flow.Read {
					add(u.Ident.Span().Start, "321", "accessing uninitialized variable %s", v.Name)
				} else {
					add(u.Ident.Span().Start, "341", "mutating uninitialized variable %s", v.Name)
				}
			}
		}
		// luacheck reports a variable with one value, or only set, or a
		// function, as an unused variable (21x, 23x), not its values
		if len(defs) < 2 || !accessed && !mutated || isFunctionVar(defs, parens) {
			continue
		}
		for _, d := range defs {
			if d.Empty || d.Used || d.Mutated {
				continue
			}
			code, what := "311", "value assigned to variable"
			if d.Init && (v.Kind == scope.KindParam || v.Kind == scope.KindSelf) {
				code, what = "312", "value of argument"
			} else if d.Init && (v.Kind == scope.KindForNum || v.Kind == scope.KindForIn) {
				code, what = "313", "value of loop variable"
			}
			how := "is unused"
			if o := d.Overwritten; o != nil {
				how = fmt.Sprintf("is overwritten on line %d before use", chunk.Pos(o.Span().Start).Line)
			}
			add(defStart(src, d), code, "%s %s %s", what, v.Name, how)
		}
	}
	// functions only used by themselves: an unused function (211) for a
	// function variable, else an unused value
	for _, d := range p.UnusedRecursive() {
		if d.Var.Name != "_" && !isFunctionVar(p.VarDefs(d.Var), parens) {
			add(defStart(src, d), "311", "value assigned to variable %s is unused", d.Var.Name)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].pos.Offset < out[j].pos.Offset })
	return out
}

// isFunctionVar is luacheck's "function variable": one value that is a
// function, or a declaration without a value and then a function.
func isFunctionVar(defs []*flow.Def, parens bool) bool {
	isFunc := func(d *flow.Def) bool {
		fe, ok := d.Value.(*ast.FunctionExpr)
		return ok && !(parens && flow.ParenFunction(d, fe))
	}
	switch len(defs) {
	case 1:
		return isFunc(defs[0])
	case 2:
		same := defs[0].Node.Block.Graph == defs[1].Node.Block.Graph
		return same && defs[0].Empty && isFunc(defs[1])
	}
	return false
}

// nodeStart is where luacheck puts a node: a condition without its
// parentheses, unless it is a call or "...".
func nodeStart(n *flow.Node) int {
	if n.Kind != flow.Condition {
		return n.Stmt.Span().Start
	}
	switch n.Expr.(type) {
	case *ast.FuncCallExpr, *ast.Comma3Expr:
		return n.Expr.OuterSpan().Start
	}
	return n.Expr.Span().Start
}

// defStart is where luacheck puts a value: at its name; for the implicit
// self of a method, at the ":" before the method's name.
func defStart(src []byte, d *flow.Def) int {
	if d.Var.Kind != scope.KindSelf || d.Name != nil {
		return d.Span().Start
	}
	fe := d.Var.Decl.(*ast.FunctionExpr)
	i := fe.Span().Start - 1 // before the "(": the name, then the ":"
	for i >= 0 && isSpace(src[i]) {
		i--
	}
	for i >= 0 && (src[i] == '_' || src[i] >= 'a' && src[i] <= 'z' || src[i] >= 'A' && src[i] <= 'Z' || src[i] >= '0' && src[i] <= '9') {
		i--
	}
	for i >= 0 && isSpace(src[i]) {
		i--
	}
	return max(i, 0)
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// nextToken returns the offset of the first token at or after off,
// skipping spaces and comments.
func nextToken(src []byte, off int) int {
	for off < len(src) {
		switch {
		case isSpace(src[off]):
			off++
		case off+1 < len(src) && src[off] == '-' && src[off+1] == '-':
			off += 2
			if lvl, ok := longBracket(src, off); ok {
				off = skipLong(src, off, lvl)
				continue
			}
			for off < len(src) && src[off] != '\n' && src[off] != '\r' {
				off++
			}
		default:
			return off
		}
	}
	return off
}

// longBracket reports whether src[off:] opens a long bracket, and its level.
func longBracket(src []byte, off int) (int, bool) {
	if off >= len(src) || src[off] != '[' {
		return 0, false
	}
	i := off + 1
	for i < len(src) && src[i] == '=' {
		i++
	}
	if i < len(src) && src[i] == '[' {
		return i - off - 1, true
	}
	return 0, false
}

// skipLong returns the offset after the long bracket of the level opened at
// off.
func skipLong(src []byte, off, lvl int) int {
	for i := off + lvl + 2; i < len(src); i++ {
		if src[i] != ']' {
			continue
		}
		j := i + 1
		for j < len(src) && src[j] == '=' {
			j++
		}
		if j < len(src) && src[j] == ']' && j-i-1 == lvl {
			return j + 1
		}
	}
	return len(src)
}
