package graph

import "slices"

// This file lists the modules a Lua runtime provides without a file.

// standardLibrary are the modules of the standard library that every runtime
// has in package.loaded before the program starts: PUC Lua 5.1 to 5.5,
// LuaJIT and Tarantool alike (read from package.loaded at startup of each).
// require returns such a module from package.loaded before it runs any
// searcher, so it never loads a file for one of these names, whatever files
// the project has. builtin_test.go checks the list on every interpreter
// present.
func standardLibrary() []string {
	return []string{"_G", "coroutine", "debug", "io", "math", "os", "package", "string", "table"}
}

// isStandardLibrary reports whether name is a module of the standard library;
// see standardLibrary.
func isStandardLibrary(name string) bool {
	return slices.Contains(standardLibrary(), name)
}
