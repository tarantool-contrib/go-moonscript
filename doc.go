// Package luamerge merges a multi-file Lua project into a single
// self-contained script, preserving require() semantics: run-once caching,
// circular dependencies, and module identity all behave the same as in the
// un-bundled project.
//
// It performs static require() analysis — modules are discovered by parsing
// source, without running the program — and registers each module into Lua's
// native package.preload table, so the bundle relies on Lua's own require for
// caching and for reporting failed loads and cycles. A module required by the
// name of a runtime's built-in module goes behind the interpreter's own
// searchers instead, so that runtime loads its own, as natively. The entry's
// code is the bundle's main chunk, so the program runs as a script does.
//
// This package is the public API, intended for embedding into other tools.
// The command-line interface lives in cmd/luamerge and is a thin wrapper over
// [Bundle].
//
//	var buf bytes.Buffer
//	opts := luamerge.DefaultOptions()
//	opts.Entry = "src/main.lua"
//	res, err := luamerge.Bundle(opts, &buf)
//	if err != nil {
//		// handle error
//	}
//	for _, w := range res.Warnings {
//		// report w
//	}
//
// See the [github.com/tarantool/go-luamerge/cmd/luamerge] command for the CLI
// built on top of this package.
package luamerge
