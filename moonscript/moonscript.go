// Package moonscript is a Go port of the MoonScript v0.8.0 compiler.
package moonscript

import (
	"github.com/tarantool-contrib/go-moonscript/moonscript/compiler"
	"github.com/tarantool-contrib/go-moonscript/moonscript/parse"
)

// Options are compile options (see moonscript.base to_lua).
type Options = compiler.Options

// Posmap maps Lua output line numbers to 1-based source byte offsets.
type Posmap = map[int]int

// ToLua compiles MoonScript source to Lua, mirroring moonscript.base.to_lua:
// errors carry the reference's message format.
func ToLua(src string, opts Options) (string, Posmap, error) {
	tree, err := parse.String(src)
	if err != nil {
		return "", nil, err
	}
	if opts.Correlate && opts.Source == "" {
		opts.Source = src
	}
	code, posmap, cerr := compiler.Tree(tree, opts)
	if cerr != nil {
		return "", nil, &Error{Msg: compiler.FormatError(cerr.Msg, cerr.Pos, src), Pos: cerr.Pos}
	}
	return code, posmap, nil
}

// Error is a compile error.
type Error struct {
	Msg string
	Pos int
}

func (e *Error) Error() string { return e.Msg }
