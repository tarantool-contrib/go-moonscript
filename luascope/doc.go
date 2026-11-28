// Package luascope statically analyzes Lua code written for Tarantool.
//
// Every use of a global variable or of a module is reported with its
// position, so the caller can allow exactly the handful of names it needs:
//
//	usage, err := globals.Analyze(src, "@migration.lua", luascope.Options{})
//	if err != nil {
//		return err
//	}
//
//	for _, access := range usage.Accesses {
//		log.Println(access.Pos, access.Path, access.Write)
//	}
//
// The Lua front end is the one of the monorepo, the gopher-lua fork in
// lua/: package parse parses a chunk with the checks of the compiler of
// Tarantool's LuaJIT (or of Lua 5.1), into the syntax tree of package ast,
// and package scope resolves every name to a local variable, an upvalue or
// a global. Parse does both, with LuaJIT's chunk names in its error
// messages; a lint pipeline that runs several analyses parses and resolves
// a chunk once and passes the tree and its resolution to each:
//
//	chunk, info, err := luascope.Parse(src, "@migration.lua", luascope.Options{})
//	usage := globals.AnalyzeChunk(chunk, info)
//	violations := checker.CheckChunk(chunk, info)
//
// Positions are those of the tree: an ast.Span of byte offsets, and the
// ast.Pos (line and column) of its start, which ast.Chunk.Lines gives.
//
// Subpackages:
//
//   - [github.com/tarantool-contrib/go-moonscript/luascope/globals] finds the globals and modules
//     that code uses.
//   - [github.com/tarantool-contrib/go-moonscript/luascope/allowlist] checks them against lists of
//     allowed names.
package luascope
