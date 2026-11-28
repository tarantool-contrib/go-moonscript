package parse

// This file exposes the require walker to the external parse_test test
// package (the "export_test.go" pattern). It is only compiled when running
// tests and does not change the package's API.

import (
	"github.com/tarantool-contrib/go-moonscript/lua/ast"
	gopherparse "github.com/tarantool-contrib/go-moonscript/lua/parse"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

// ErrUnsupportedNodeForTest is errUnsupportedNode, exported for tests.
var ErrUnsupportedNodeForTest = errUnsupportedNode

// FindRequiresForTest is findRequires of a tree read from a source itself,
// exported for tests: a node type the walker does not know cannot come out of
// the parser, only be built by hand.
func FindRequiresForTest(chunk []ast.Stmt) ([]RequireInfo, error) {
	return findRequires(chunk, nil, nil)
}

// RewriteForTest is rewrite, exported for tests: the copy of a source that
// the parser reads when it refuses the source.
func RewriteForTest(src []byte) ([]byte, error) {
	tokens, err := significantTokens(src)
	if err != nil {
		return nil, err
	}

	return rewrite(src, tokens).text, nil
}

// ParseCopyForTest returns what Parse finds in src when the parser reads its
// copy (see rewrite), as for a source the parser refuses, whatever src is.
func ParseCopyForTest(src []byte, filename string) (Result, error) {
	tokens, err := significantTokens(src)
	if err != nil {
		return Result{}, err
	}

	copied := rewrite(src, tokens)

	chunk, err := gopherparse.ParseChunk(copied.text, filename, luaJIT(true))
	if err != nil {
		return Result{Requires: scanRequires(src, tokens), Fallback: newFallback(err, tokens)}, nil
	}

	return walk(chunk, copied)
}

// ScanRequiresForTest runs the token scanner on src, which Parse runs only on
// a source the parser cannot read.
func ScanRequiresForTest(src []byte) ([]RequireInfo, error) {
	tokens, err := significantTokens(src)
	if err != nil {
		return nil, err
	}

	return scanRequires(src, tokens), nil
}

// LiteralValueForTest returns the value of the string literal lit, the text
// of one String or LongString token, and whether lit is one.
func LiteralValueForTest(lit []byte) (string, bool) {
	tokens, err := lex.New(lit).Tokens()
	if err != nil || len(tokens) != 1 || tokens[0].Start != 0 || tokens[0].End != len(lit) {
		return "", false
	}

	kind := tokens[0].Kind
	if kind != lex.String && kind != lex.LongString {
		return "", false
	}

	return string(stringValue(lit, kind)), true
}
