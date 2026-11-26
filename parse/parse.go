// Package parse is a port of the MoonScript v0.8.0 parser.
package parse

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/ast"
)

// String parses MoonScript source into a statement list, mirroring
// moonscript.parse.string: on failure it returns a formatted error message.
func String(src string) (tree *ast.Node, err error) {
	return stringWith(src, match)
}

// match runs the generated parser (parser_gen.go).
func match(input string) (vals []any, ok bool, failPos int) {
	p := newGenParser(input)
	defer p.release()
	if !p.runPackrat() {
		return nil, false, p.furthest + 1
	}
	return slices.Clone(p.materialize()), true, 0
}

// matcher runs a grammar on input. On success it returns the materialized
// top-level capture values. On failure it returns ok=false and the 1-based
// furthest failure position. Callback panics propagate.
type matcher func(input string) (vals []any, ok bool, failPos int)

func stringWith(src string, match matcher) (tree *ast.Node, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		switch e := r.(type) {
		case AssignError:
			if p := ast.PosOf(e.Node); p != 0 {
				err = &Error{Msg: FormatError(src, p, e.Msg)}
			} else {
				err = &Error{Msg: "failed to parse: " + e.Msg}
			}
		case depthError:
			err = &Error{Msg: e.Error()}
		default:
			panic(r)
		}
		tree = nil
	}()

	// The reference parser (pgen's C target) takes the input with
	// lua_tostring and measures it with strlen, so everything from the
	// first NUL byte on is invisible to the grammar. Error messages are still
	// formatted against the full source by moonscript.parse.
	input := src
	if i := strings.IndexByte(input, 0); i >= 0 {
		input = input[:i]
	}

	if len(input) > math.MaxInt32 {
		// capture log offsets are 32-bit
		return nil, &Error{Msg: "failed to parse: input too large"}
	}

	vals, ok, failPos := match(input)
	if !ok {
		return nil, &Error{Msg: FormatError(src, failPos, "failed to parse")}
	}
	if len(vals) == 0 {
		return nil, &Error{Msg: "failed to parse"}
	}
	n, isNode := vals[0].(*ast.Node)
	if !isNode {
		return nil, &Error{Msg: fmt.Sprintf("failed to parse: unexpected result %v", vals[0])}
	}
	return n, nil
}

// Error is a parse error with a message formatted like the reference.
type Error struct {
	Msg string
}

func (e *Error) Error() string { return e.Msg }
