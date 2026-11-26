package compiler

// Port of moonscript/transform/statements.moon, comprehension.moon and
// accumulator.moon.

import "github.com/tarantool-contrib/go-moonscript/ast"

// Run is a special statement node that lets a function run and mutate the
// state of the compiler. ntype(run) == "run".
type Run struct {
	Fn  func(scope *Block)
	Pos int
}

func NewRun(fn func(scope *Block)) *Run { return &Run{Fn: fn} }

func (r *Run) NType() string { return "run" }

func (r *Run) Call(scope *Block) { r.Fn(scope) }

// lastStm extracts the last statement from an array of statements; it is
// group aware. Returns the statement, its index and the list it came from.
func lastStm(stms *ast.Node) (any, int, *ast.Node) {
	lastExpID := 0
	for i := stms.Len(); i >= 1; i-- {
		stm := stms.At(i)
		if _, isRun := stm.(*Run); truthy(stm) && !isRun {
			if ntype(stm) == "group" {
				return lastStm(stm.(*ast.Node).At(2).(*ast.Node))
			}
			lastExpID = i
			break
		}
	}
	return stms.At(lastExpID), lastExpID, stms
}

// transformLastStm transforms the last stm in a list of stms; will puke on
// group.
func transformLastStm(stms *ast.Node, fn RetFn) *ast.Node {
	_, lastIdx, inner := lastStm(stms)
	if inner != stms {
		panic("cannot transform last node in group")
	}
	out := node()
	for i, stm := range stms.Items {
		if stm == nil {
			break // ipairs
		}
		if i+1 == lastIdx {
			out.Append(node("transform", stm, fn))
		} else {
			out.Append(stm)
		}
	}
	return out
}

func chainIsStub(chain *ast.Node) bool {
	stub := chain.Last()
	return stub != nil && ntype(stub) == "colon"
}

// nodes that a continue statement binds to, a continue found inside one of
// these belongs to that construct and not the enclosing loop
var continueBoundaries = set("fndef", "while", "for", "foreach", "comprehension", "tblcomprehension")

// findContinues collects the continue nodes in a body that bind to the
// enclosing loop
func findContinues(tbl *ast.Node, out []*ast.Node) []*ast.Node {
	for _, item := range tbl.Items {
		if item == nil {
			break
		}
		if n, ok := item.(*ast.Node); ok {
			if s, _ := n.At(1).(string); s == "continue" {
				out = append(out, n)
			} else if !continueBoundaries[n.Kind()] {
				out = findContinues(n, out)
			}
		}
	}
	return out
}

// hasVarargs: does a body reference the enclosing function's varargs. Nested
// functions have their own varargs, string nodes hold literal text at string
// positions
func hasVarargs(tbl *ast.Node) bool {
	for _, item := range tbl.Items {
		if item == nil {
			break
		}
		switch t := item.(type) {
		case string:
			if t == "..." {
				return true
			}
		case *ast.Node:
			switch t.Kind() {
			case "fndef":
				continue
			case "string":
				for i := 3; i <= t.Len(); i++ {
					if part, ok := t.At(i).(*ast.Node); ok && hasVarargs(part) {
						return true
					}
				}
			default:
				if hasVarargs(t) {
					return true
				}
			}
		}
	}
	return false
}

func implicitlyReturn(scope *Block) RetFn {
	isTop := true
	var fn RetFn
	fn = func(stm any) any {
		t := ntype(stm)

		// expand decorated
		if t == "decorated" {
			stm = scope.TransformStatement(stm)
			t = ntype(stm)
		}

		if cascading[t] {
			isTop = false
			return scope.TransformStatement(stm, fn)
		} else if manualReturn[t] || !isValue(stm) {
			// remove blank return statement
			if isTop && t == "return" && stm.(*ast.Node).At(2) == "" {
				return noop()
			}
			return stm
		}
		if t == "comprehension" && !comprehensionHasValue(stm.(*ast.Node)) {
			return stm
		}
		return node("return", stm)
	}
	return fn
}

// --- comprehension.moon ---------------------------------------------------------

func constructComprehension(inner *ast.Node, clauses *ast.Node) any {
	current := inner
	for i := clauses.Len(); i >= 1; i-- {
		clause := clauses.At(i).(*ast.Node)
		var stm *ast.Node
		switch t := clause.Kind(); t {
		case "for":
			stm = node("for", clause.At(2), clause.At(3), current)
		case "foreach":
			stm = node("foreach", clause.At(2), node(clause.At(3)), current)
		case "when":
			stm = node("if", clause.At(2), current)
		default:
			panic("Unknown comprehension clause: " + t)
		}
		current = node(stm)
	}
	return current.At(1)
}

func comprehensionHasValue(comp *ast.Node) bool {
	return isValue(comp.At(2))
}

// --- accumulator.moon -------------------------------------------------------------

// isSingular: is a body a single expression/statement
func isSingular(body *ast.Node) any {
	if body.Len() != 1 {
		return nil
	}
	if body.Kind() == "group" {
		return isSingular(body.At(2).(*ast.Node))
	}
	return body.At(1)
}

type accumulator struct {
	accumName, valueName, lenName *NameProxy
}

var accumulatorBodyIdx = map[string]int{"for": 4, "while": 3, "foreach": 4}

func newAccumulator() *accumulator {
	return &accumulator{
		accumName: NewNameProxy("accum"),
		valueName: NewNameProxy("value"),
		lenName:   NewNameProxy("len"),
	}
}

// convert wraps node and mutates body
func (a *accumulator) convert(n *ast.Node) *ast.Node {
	index := accumulatorBodyIdx[n.Kind()]
	n.Set(index, a.mutateBody(n.At(index).(*ast.Node)))
	return a.wrap(n, "block_exp")
}

// wrap the node into a block_exp (or a group)
func (a *accumulator) wrap(n any, groupType string) *ast.Node {
	var last any = noop()
	if groupType == "block_exp" {
		last = a.accumName
	}
	body := node(
		buildAssignOne(a.accumName, buildTable()),
		buildAssignOne(a.lenName, 1),
		n,
		last,
	)
	if groupType == "block_exp" {
		return buildBlockExp(body)
	}
	return buildGroup(body)
}

// mutateBody mutates the body of a loop construct to save last value into
// accumulator
func (a *accumulator) mutateBody(body *ast.Node) *ast.Node {
	// shortcut to write simpler code if body is a single expression
	single := isSingular(body)
	var val any
	if single != nil && isValue(single) {
		body = node()
		val = single
	} else {
		body = transformLastStm(body, func(n any) any {
			if isValue(n) {
				return buildAssignOne(a.valueName, n)
			}
			// just ignore it
			return buildGroup(node(
				node("declare", node(a.valueName)),
				n,
			))
		})
		val = a.valueName
	}

	update := node(
		buildAssignOne(proxyIndex(a.accumName, a.lenName), val),
		node("update", a.lenName, "+=", 1),
	)
	body.Append(buildGroup(update))
	return body
}

func defaultAccumulator(scope *Block, n *ast.Node, extra ...any) any {
	return newAccumulator().convert(n)
}
