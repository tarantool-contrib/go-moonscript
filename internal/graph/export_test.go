package graph

// This file exposes the lists of modules runtimes provide to the external
// graph_test test package (the "export_test.go" pattern), so a test can check
// them against the interpreters themselves. It is only compiled when running
// tests and does not change the package's API.

// StandardLibraryForTest is standardLibrary, exported for tests.
func StandardLibraryForTest() []string {
	return standardLibrary()
}
