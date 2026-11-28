package parse

// This file exposes the require walker to the external parse_test test
// package (the "export_test.go" pattern). It is only compiled when running
// tests and does not change the package's API.

import (
	"github.com/yuin/gopher-lua/ast"

	"github.com/tarantool/go-luamerge/internal/lex"
)

// ErrUnsupportedNodeForTest is errUnsupportedNode, exported for tests.
var ErrUnsupportedNodeForTest = errUnsupportedNode

// FindRequiresForTest is findRequires of a chunk read from anything but a
// marked copy, exported for tests: a node type the walker does not know
// cannot come out of the parser, only be built by hand.
func FindRequiresForTest(chunk []ast.Stmt) ([]RequireInfo, error) {
	return findRequires(chunk, nil)
}

// NormalizeForTest is normalize, exported for tests: the copy of a source
// that gopher-lua reads.
func NormalizeForTest(src []byte) ([]byte, error) {
	return normalize(src)
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
