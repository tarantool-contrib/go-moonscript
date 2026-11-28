package graph

// This file exposes the lists of modules runtimes provide to the external
// graph_test test package (the "export_test.go" pattern), so a test can check
// them against the interpreters themselves. It is only compiled when running
// tests and does not change the package's API.

// StandardLibraryForTest is standardLibrary, exported for tests.
func StandardLibraryForTest() []string {
	return standardLibrary()
}

// RuntimeNamesForTest is runtimeNames, exported for tests: the name of every
// runtime the lists know.
func RuntimeNamesForTest() []string {
	return runtimeNames()
}

// BuiltinModulesForTest is builtinModules, exported for tests, with the names
// of the runtimes (see runtimeNames) that provide each module.
func BuiltinModulesForTest() map[string][]string {
	modules := make(map[string][]string)
	for name, providers := range builtinModules() {
		modules[name] = providers.names()
	}

	return modules
}
