// Package allowlist checks the global variables and modules Lua code uses
// against lists of allowed names.
package allowlist

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	"github.com/tarantool-contrib/go-moonscript/lua/scope"
	"github.com/tarantool-contrib/go-moonscript/luascope"
	"github.com/tarantool-contrib/go-moonscript/luascope/globals"
)

// ErrInvalidRule is returned for a malformed rule.
var ErrInvalidRule = errors.New("invalid rule")

// Rules are the allowed modules and global variables.
//
// An entry is a dot-separated name that allows exactly that module or
// global path: "crud" allows require("crud"), "string" allows reading the
// string table itself, "box.schema.space.create" allows calling it.
//
// A "*" element matches any one field or module name, including a field
// indexed with a non-constant key: "box.space.*.select" allows
// box.space.users:select() and box.space[name]:select(). A "*" at the end
// matches one or more elements: "box.space.*" allows box.space.users and
// box.space.users:replace(), but not box.space itself; "crud.*" allows
// require("crud.common.utils"), but not require("crud"). A "*" inside a
// name, like "create_*", is not supported.
//
// Writing to a global variable or to a field of one is never allowed.
type Rules struct {
	Modules []string
	Globals []string
}

// Kind is the kind of a violation.
type Kind int

const (
	// KindModule is a require of a module that is not allowed.
	KindModule Kind = iota + 1
	// KindGlobal is a use of a global variable that is not allowed.
	KindGlobal
)

// Violation is a use of a module or a global variable the rules forbid.
type Violation struct {
	Kind Kind
	// Span is the span of the global name the use starts with, Pos the
	// position of its start (see globals.Access and globals.Require).
	Span ast.Span
	Pos  ast.Pos
	// Name is the module name or the global path.
	Name string
	// Msg describes the violation.
	Msg string
}

// String returns "line:col: message".
func (v Violation) String() string {
	return v.Pos.String() + ": " + v.Msg
}

// wildcard matches any one element, or one or more at the end of a pattern.
const wildcard = "*"

// pattern is a compiled rule entry.
type pattern []string

// compile parses rule entries.
func compile(entries []string) ([]pattern, error) {
	patterns := make([]pattern, 0, len(entries))

	for _, e := range entries {
		p := pattern(strings.Split(e, "."))
		if slices.ContainsFunc(p, invalidElement) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidRule, e)
		}

		patterns = append(patterns, p)
	}

	return patterns, nil
}

func invalidElement(s string) bool {
	return s == "" || s != wildcard && strings.Contains(s, wildcard)
}

// matches reports whether the path matches the pattern.
func (p pattern) matches(path globals.Path) bool {
	for i, name := range p {
		if name == wildcard && i == len(p)-1 {
			return len(path) > i
		}

		if i == len(path) {
			return false
		}

		if name != wildcard && (path[i].Dynamic || path[i].Name != name) {
			return false
		}
	}

	return len(path) == len(p)
}

func allowed(patterns []pattern, path globals.Path) bool {
	return slices.ContainsFunc(patterns, func(p pattern) bool { return p.matches(path) })
}

// Checker checks chunks against rules.
type Checker struct {
	modules []pattern
	globals []pattern
}

// NewChecker compiles the rules.
func NewChecker(rules Rules) (*Checker, error) {
	modules, err := compile(rules.Modules)
	if err != nil {
		return nil, fmt.Errorf("modules: %w", err)
	}

	globals, err := compile(rules.Globals)
	if err != nil {
		return nil, fmt.Errorf("globals: %w", err)
	}

	return &Checker{modules: modules, globals: globals}, nil
}

// Check parses and resolves a chunk with luascope.Parse and returns its
// violations ordered by position. A syntax error is a *parse.Error.
func (c *Checker) Check(src []byte, name string, opts luascope.Options) ([]Violation, error) {
	chunk, info, err := luascope.Parse(src, name, opts)
	if err != nil {
		return nil, err
	}

	return c.CheckChunk(chunk, info), nil
}

// CheckChunk returns the violations of a chunk that is already parsed and
// resolved, ordered by position: info must be the resolution of chunk, see
// globals.AnalyzeChunk.
func (c *Checker) CheckChunk(chunk *ast.Chunk, info *scope.Info) []Violation {
	usage := globals.AnalyzeChunk(chunk, info)

	var vs []Violation

	for _, r := range usage.Requires {
		if v, ok := c.checkRequire(r); ok {
			vs = append(vs, v)
		}
	}

	for _, a := range usage.RequireValues {
		vs = append(vs, Violation{
			Kind: KindModule, Span: a.Span, Pos: a.Pos, Name: globals.RequireName,
			Msg: "require used as a value; only calls with a constant module name are allowed",
		})
	}

	for _, a := range usage.Accesses {
		if v, ok := c.checkAccess(a); ok {
			vs = append(vs, v)
		}
	}

	slices.SortStableFunc(vs, func(a, b Violation) int {
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line - b.Pos.Line
		}

		return a.Pos.Column - b.Pos.Column
	})

	return vs
}

func (c *Checker) checkRequire(r globals.Require) (Violation, bool) {
	if r.Dynamic {
		return Violation{
			Kind: KindModule, Span: r.Span, Pos: r.Pos, Name: globals.RequireName,
			Msg: "require with a non-constant module name",
		}, true
	}

	path := make(globals.Path, 0, strings.Count(r.Module, ".")+1)
	for name := range strings.SplitSeq(r.Module, ".") {
		path = append(path, globals.Segment{Name: name})
	}

	if allowed(c.modules, path) {
		return Violation{}, false
	}

	return Violation{
		Kind: KindModule, Span: r.Span, Pos: r.Pos, Name: r.Module,
		Msg: fmt.Sprintf("module '%s' is not allowed", r.Module),
	}, true
}

func (c *Checker) checkAccess(a globals.Access) (Violation, bool) {
	name := a.Path.String()

	switch {
	case a.Write:
		return Violation{
			Kind: KindGlobal, Span: a.Span, Pos: a.Pos, Name: name,
			Msg: fmt.Sprintf("assignment to global variable '%s'", name),
		}, true
	case allowed(c.globals, a.Path):
		return Violation{}, false
	default:
		return Violation{
			Kind: KindGlobal, Span: a.Span, Pos: a.Pos, Name: name,
			Msg: fmt.Sprintf("global variable '%s' is not allowed", name),
		}, true
	}
}
