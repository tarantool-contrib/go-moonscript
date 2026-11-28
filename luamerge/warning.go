package luamerge

import "github.com/tarantool-contrib/go-moonscript/luamerge/internal/graph"

// WarningKind classifies a Warning; see the Warn* constants.
type WarningKind int

// The kinds of Warning. They are declared in the order of the internal graph
// package's kinds, which Bundle converts by value.
const (
	// WarnDynamicRequire is a require whose argument is not a literal
	// string, so its module cannot be found without running the program.
	WarnDynamicRequire WarningKind = iota
	// WarnSkipped is a module not embedded on purpose: required by a name
	// that Options.SkipPackages matches, or listed in Options.IncludePackages
	// but matched by Options.SkipPackages or a standard library module.
	WarnSkipped
	// WarnUnresolved is a module that cannot be found, required by the
	// program or listed in Options.IncludePackages. In strict mode Bundle
	// returns an *UnresolvedError instead, unless the require is called in
	// protected mode (`pcall(require, "x")`, `xpcall(require, h, "x")`): the
	// program is written to run on without that module.
	WarnUnresolved
	// WarnCModule is a module found as a C module (.so or .dll), which cannot
	// be embedded.
	WarnCModule
	// WarnNonLua is a module found as a file other than a .lua one, which
	// cannot be embedded.
	WarnNonLua
	// WarnIndirectRequire is require used other than by a call — aliased
	// (`local r = require`), stored or passed on as a value — so the modules
	// loaded through it are not embedded; list them in
	// Options.IncludePackages.
	WarnIndirectRequire
	// WarnShadowsBuiltin is a module of the project required by the name of
	// a module some runtimes have built in (a pure-Lua bit.lua or utf8.lua,
	// a csv.lua on Tarantool). The module is embedded, but where the runtime
	// has its own module of that name require loads that one, as natively;
	// the message names those runtimes.
	WarnShadowsBuiltin
	// WarnTokenScan is a module the parser cannot read, usually for a syntax
	// error. It is embedded, and its requires were found by scanning its
	// tokens: the calls of require are found, but a local named require is
	// taken for the global one. Line is where the parser failed.
	WarnTokenScan
)

// String returns the name of the kind, such as "dynamic-require", or
// "unknown" for a value that is none of the Warn* constants.
func (k WarningKind) String() string {
	switch k {
	case WarnDynamicRequire:
		return "dynamic-require"
	case WarnSkipped:
		return "skipped"
	case WarnUnresolved:
		return "unresolved"
	case WarnCModule:
		return "c-module"
	case WarnNonLua:
		return "non-lua"
	case WarnIndirectRequire:
		return "indirect-require"
	case WarnShadowsBuiltin:
		return "shadows-builtin"
	case WarnTokenScan:
		return "token-scan"
	default:
		return "unknown"
	}
}

// Warning is a non-fatal issue found while building the dependency graph. For
// most kinds the require is left to be resolved at runtime instead of being
// embedded; a WarnShadowsBuiltin module is embedded.
type Warning struct {
	// Kind classifies the warning.
	Kind WarningKind
	// Module is the name of the module, empty when it is not known: for a
	// dynamic or an indirect require.
	Module string
	// File is the file of the require, empty for a module of
	// Options.IncludePackages.
	File string
	// Line is the line of the require in File, 0 without a file.
	Line int
	// Message describes the warning for a reader. It does not repeat File and
	// Line, which a caller shows alongside.
	Message string
}

// warningsFromGraph returns the warnings of the internal graph package as
// Warnings, nil for none.
func warningsFromGraph(warnings []graph.Warning) []Warning {
	if warnings == nil {
		return nil
	}

	public := make([]Warning, len(warnings))
	for i, w := range warnings {
		public[i] = Warning{
			Kind:    WarningKind(w.Kind),
			Module:  w.Module,
			File:    w.File,
			Line:    w.Line,
			Message: w.Message,
		}
	}

	return public
}
