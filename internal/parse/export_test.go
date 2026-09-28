package parse

// This file exposes the require walker to the external parse_test test
// package (the "export_test.go" pattern). It is only compiled when running
// tests and does not change the package's API.

import "github.com/yuin/gopher-lua/ast"

// ErrUnsupportedNodeForTest is errUnsupportedNode, exported for tests.
var ErrUnsupportedNodeForTest = errUnsupportedNode

// FindRequiresForTest is findRequires, exported for tests: a node type the
// walker does not know cannot come out of the parser, only be built by hand.
func FindRequiresForTest(chunk []ast.Stmt) ([]RequireInfo, error) {
	return findRequires(chunk)
}

// NormalizeForTest is normalize, exported for tests: the copy of a source
// that gopher-lua reads.
func NormalizeForTest(src []byte) ([]byte, error) {
	return normalize(src)
}
