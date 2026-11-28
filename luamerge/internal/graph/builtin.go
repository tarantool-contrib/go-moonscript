package graph

import (
	"slices"
	"strings"
)

// This file lists the modules a Lua runtime provides without a file: require
// returns them from package.loaded, or finds them with a searcher of the
// runtime's own that runs before any searcher that looks for a file. Every
// list was read from the interpreters themselves: package.loaded and
// package.preload at startup, and Tarantool's table of built-in modules.
// builtin_test.go checks them against every interpreter present, both ways:
// what a list attributes to a runtime, the runtime provides, and what a
// runtime provides, a list attributes to it.
//
// Measured: PUC Lua 5.1 to 5.4 of Alpine 3.24 and Ubuntu 22.04 and 24.04,
// Lua 5.5.1, LuaJIT 2.1 as released in 2017 (Ubuntu 22.04) and of 2023 and
// 2026, OpenResty's LuaJIT of 2025 (Alpine), Tarantool 2.11.5, 3.0.2 (static
// build), 3.8.1 (static build) and 3.9.0.

// standardLibrary are the modules of the standard library that every runtime
// has in package.loaded before the program starts: PUC Lua 5.1 to 5.5,
// LuaJIT and Tarantool alike. require returns such a module from
// package.loaded before it runs any searcher, so it never loads a file for
// one of these names, whatever files the project has.
func standardLibrary() []string {
	return []string{"_G", "coroutine", "debug", "io", "math", "os", "package", "string", "table"}
}

// isStandardLibrary reports whether name is a module of the standard library;
// see standardLibrary.
func isStandardLibrary(name string) bool {
	return slices.Contains(standardLibrary(), name)
}

// runtimes is a set of Lua runtimes, one bit each.
type runtimes uint

const (
	// lua51 is PUC Lua 5.1, which has nothing beyond the standard library.
	lua51 runtimes = 1 << iota
	lua52
	lua53
	lua54
	lua55
	// luaJIT is LuaJIT 2.1, OpenResty's branch of it included.
	luaJIT
	// luaJIT2021 is LuaJIT 2.1 as of 2021 and later, which has the string
	// buffer library. LuaJIT 2.1.0-beta3 as released in 2017, still packaged
	// by Ubuntu 22.04, has none, and the snapshots of 2021 to 2023 report the
	// same version.
	luaJIT2021
	// openResty is OpenResty's branch of LuaJIT, which e.g. Alpine packages
	// as luajit.
	openResty
	// tarantool is Tarantool 2.11 and later: the modules every release has.
	tarantool
	// tarantool3 is Tarantool 3. A 3.x release has only some of the modules
	// listed for it: the list is the union of what 3.0 to 3.9 have, as 3.x
	// releases add built-in modules and rename a few.
	tarantool3
	// tarantoolStatic is Tarantool's static build, such as the one in the
	// tarantool/tarantool Docker image.
	tarantoolStatic
)

// runtimeNames names each runtime, in the order of their bits.
func runtimeNames() []string {
	return []string{
		"Lua 5.1", "Lua 5.2", "Lua 5.3", "Lua 5.4", "Lua 5.5",
		"LuaJIT", "LuaJIT since 2021", "OpenResty's LuaJIT",
		"Tarantool", "Tarantool 3", "Tarantool's static build",
	}
}

// String names the runtimes in r, as in "Lua 5.4, Lua 5.5 and Tarantool".
func (r runtimes) String() string {
	names := r.names()

	switch len(names) {
	case 0:
		return "no runtime"
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// names returns the names of the runtimes in r.
func (r runtimes) names() []string {
	var names []string

	for i, name := range runtimeNames() {
		if r&(runtimes(1)<<i) != 0 {
			names = append(names, name)
		}
	}

	return names
}

// builtinModules maps every module that some runtime provides without a
// file, beyond the standard library, to the runtimes that do.
func builtinModules() map[string]runtimes {
	modules := map[string]runtimes{
		// PUC Lua: the bitwise library of Lua 5.2, which the default build of
		// Lua 5.3 keeps (LUA_COMPAT_5_2), and the UTF-8 library of Lua 5.3 and
		// later. Tarantool has a utf8 module of its own.
		"bit32": lua52 | lua53,
		"utf8":  lua53 | lua54 | lua55 | tarantool,
		// LuaJIT's extensions: bit, jit and jit.opt in package.loaded, the
		// others in package.preload. Tarantool's LuaJIT has them too, except
		// string.buffer, which LuaJIT has had since 2021.
		"bit":           luaJIT | tarantool,
		"ffi":           luaJIT | tarantool,
		"jit":           luaJIT | tarantool,
		"jit.opt":       luaJIT | tarantool,
		"jit.profile":   luaJIT | tarantool,
		"jit.util":      luaJIT | tarantool,
		"string.buffer": luaJIT2021,
		"table.clear":   luaJIT | tarantool,
		"table.new":     luaJIT | tarantool,
		// OpenResty's branch of LuaJIT preloads more.
		"table.clone":    openResty,
		"table.isarray":  openResty,
		"table.isempty":  openResty,
		"table.nkeys":    openResty,
		"thread.exdata":  openResty,
		"thread.exdata2": openResty,
		// Tarantool's static build loads a zlib binding at startup.
		"zlib": tarantoolStatic,
	}

	for _, name := range tarantoolModules() {
		modules[name] |= tarantool
	}

	for _, name := range tarantool3Modules() {
		modules[name] |= tarantool3
	}

	return modules
}

// tarantoolModules are the modules only Tarantool has built in, and every
// release of it from 2.11 on: those in its package.loaded and package.preload
// at startup, and those in its table of built-in modules
// (package.loaded["internal.loaders"].builtin), which its first searcher
// consults after package.preload and before any file. Some of the latter
// reach package.loaded only once required, such as csv.
func tarantoolModules() []string {
	return []string{
		"box", "buffer", "checks", "checks.version", "clock", "compat", "console", "console.lib",
		"crc32.internal", "crypto", "csv", "datetime", "decimal", "digest", "digest.lib", "errno",
		"errno.lib", "error", "fiber", "fio", "fun", "help", "help.en_US", "http.client", "http.client.lib",
		"iconv", "internal.argparse", "internal.dobytecode", "internal.dojitcmd", "internal.loaders",
		"internal.minifio", "internal.pairs", "internal.print", "internal.trigger", "internal.tweaks",
		"internal.utils", "internal.xml", "jit.bc", "jit.bcsave", "jit.dis_arm64", "jit.dis_x64",
		"jit.dis_x86", "jit.dump", "jit.p", "jit.v", "jit.vmdef", "jit.zone", "json", "key_def", "log",
		"luadebug", "memprof", "memprof.humanize", "memprof.parse", "memprof.process", "merger", "metrics",
		"metrics.api", "metrics.cartridge.failover", "metrics.cartridge.issues", "metrics.cfg",
		"metrics.collectors.counter", "metrics.collectors.gauge", "metrics.collectors.histogram",
		"metrics.collectors.shared", "metrics.collectors.summary", "metrics.const",
		"metrics.http_middleware", "metrics.plugins.graphite", "metrics.plugins.json",
		"metrics.plugins.prometheus", "metrics.psutils.cpu", "metrics.psutils.psutils_linux",
		"metrics.quantile", "metrics.registry", "metrics.stash", "metrics.tarantool",
		"metrics.tarantool.clock", "metrics.tarantool.cpu", "metrics.tarantool.event_loop",
		"metrics.tarantool.fibers", "metrics.tarantool.info", "metrics.tarantool.luajit",
		"metrics.tarantool.memory", "metrics.tarantool.memtx", "metrics.tarantool.network",
		"metrics.tarantool.operations", "metrics.tarantool.replicas", "metrics.tarantool.runtime",
		"metrics.tarantool.slab", "metrics.tarantool.spaces", "metrics.tarantool.system",
		"metrics.tarantool.vinyl", "metrics.utils", "metrics.version", "misc", "misc.memprof",
		"misc.sysprof", "msgpack", "msgpackffi", "net.box", "net.box.lib", "pickle", "popen", "pwd",
		"socket", "socket.lib", "strict", "swim", "swim.lib", "sysprof", "sysprof.parse", "tap",
		"tarantool", "timezones", "title", "uri", "uri.lib", "utils.avl", "utils.bufread", "utils.evread",
		"utils.symtab", "uuid", "xlog", "xlog.lib", "yaml",
	}
}

// tarantool3Modules are the built-in modules of Tarantool 3 that 2.11 does
// not have, found the same way. A 3.x release has only some of them: 3.0
// lacks the 31 that later releases added, and has internal.mkversion and
// internal.config.utils.schema, which they do not.
func tarantool3Modules() []string {
	return []string{
		"config", "experimental.config.utils.schema", "experimental.connpool", "experimental.threads",
		"internal.alloc", "internal.asan", "internal.config.applier.app",
		"internal.config.applier.autoexpel", "internal.config.applier.box_cfg",
		"internal.config.applier.box_status", "internal.config.applier.compat",
		"internal.config.applier.connpool", "internal.config.applier.console",
		"internal.config.applier.credentials", "internal.config.applier.fiber",
		"internal.config.applier.lua", "internal.config.applier.mkdir", "internal.config.applier.roles",
		"internal.config.applier.runtime_priv", "internal.config.applier.session_settings",
		"internal.config.applier.sharding", "internal.config.cluster_config", "internal.config.configdata",
		"internal.config.descriptions", "internal.config.discriminators", "internal.config.instance_config",
		"internal.config.source.env", "internal.config.source.file", "internal.config.utils.aboard",
		"internal.config.utils.expression", "internal.config.utils.expression_lexer",
		"internal.config.utils.file", "internal.config.utils.funcutils", "internal.config.utils.log",
		"internal.config.utils.network", "internal.config.utils.odict", "internal.config.utils.schema",
		"internal.config.utils.snapshot", "internal.config.utils.tabulate",
		"internal.config.utils.textutils", "internal.config.utils.units", "internal.config.validators",
		"internal.healthcheck", "internal.healthcheck.defaults", "internal.mkversion",
		"internal.net.replicaset", "internal.protobuf.wireformat", "metrics.cartridge.config",
		"metrics.tarantool.config", "metrics.tarantool.schema", "protobuf", "roles.recovery-point-manager",
		"roles.recovery-point-manager.backends.net-replicaset", "trigger", "ulid", "varbinary", "version",
	}
}

// builtinTable is builtinModules, built once.
//
//nolint:gochecknoglobals // built once at init, read-only for the rest of the run
var builtinTable = builtinModules()

// builtinRuntimes returns the runtimes that provide the module name without a
// file, beyond the standard library: none for a module no runtime has.
func builtinRuntimes(name string) runtimes {
	return builtinTable[name]
}
