package flow

import "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"

// LuacheckOptions are the Options with luacheck's approximations: its
// cross-resolution of closures, parenthesized functions, ends of scopes
// and reads in circular assignments.
func LuacheckOptions(closures, parens, scopes, circular bool) *Options {
	return &Options{luacheckClosures: closures, luacheckParens: parens, luacheckScopes: scopes, luacheckCircular: circular}
}

// ParenFunction reports whether a def's value is a function that luacheck
// takes as none.
func ParenFunction(d *Def, fe *ast.FunctionExpr) bool { return parenFunction(d, fe) }

// GraphsOnly are the Options that build the graphs without the data flow.
func GraphsOnly() *Options { return &Options{graphsOnly: true} }

// Misses returns the lookups of the resolution that the builder didn't find
// in its order.
func (p *Program) Misses() int { return p.misses }
