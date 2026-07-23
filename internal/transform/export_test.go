package transform

// This file exposes the package's unexported transformer types to the
// external transform_test test package (the "export_test.go" pattern). It is
// only compiled when running tests and does not change the package's public
// API.

// CommentTransformerForTest is commentTransformer, exported for tests.
type CommentTransformerForTest = commentTransformer

// EmptyLineTransformerForTest is emptyLineTransformer, exported for tests.
type EmptyLineTransformerForTest = emptyLineTransformer

// MinifyTransformerForTest is minifyTransformer, exported for tests.
type MinifyTransformerForTest = minifyTransformer

// ShebangTransformerForTest is shebangTransformer, exported for tests.
type ShebangTransformerForTest = shebangTransformer
