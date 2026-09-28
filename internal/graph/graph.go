// Package graph builds an in-memory dependency graph of Lua modules reachable
// from an entry file, following require() calls and --include packages.
package graph

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/go-luamerge/internal/config"
	"github.com/tarantool/go-luamerge/internal/lex"
	"github.com/tarantool/go-luamerge/internal/parse"
	"github.com/tarantool/go-luamerge/internal/resolve"
)

// WarningKind classifies a non-fatal issue found while building the graph.
type WarningKind int

const (
	// WarnDynamicRequire is a require() with a non-literal argument that can't
	// be resolved statically.
	WarnDynamicRequire WarningKind = iota
	// WarnSkipped is a require() excluded by a skip pattern.
	WarnSkipped
	// WarnUnresolved is a require() whose module could not be found. Strict
	// mode returns an *UnresolvedError instead, unless the require is called
	// in protected mode (`pcall(require, "x")`, `xpcall(require, h, "x")`):
	// the program is written to run on without that module.
	WarnUnresolved
	// WarnCModule is a require() that resolved to a C module (.so/.dll).
	WarnCModule
	// WarnNonLua is a require() that resolved to a non-.lua file.
	WarnNonLua
	// WarnIndirectRequire is a use of require other than a call: require
	// aliased (`local r = require`), stored in a table or passed on as a value.
	// The calls made through it can't be seen, so the modules they load are
	// not embedded. `local require = require` is not one: calls through it are
	// still found by name.
	WarnIndirectRequire
	// WarnShadowsBuiltin is a module of the project required by the name of a
	// module some runtimes have built in, such as a pure-Lua bit.lua or
	// utf8.lua, or a csv.lua on Tarantool. Where the runtime has its own
	// module of that name, require loads that one and not the file, natively
	// and from the bundle alike; elsewhere it loads the module of the
	// project. The module is embedded, and Message names those runtimes.
	WarnShadowsBuiltin
	// WarnTokenScan is a module the parser cannot read, usually for a syntax
	// error: its requires were found by the token scanner instead. It finds
	// the calls of require the parser does, but takes a local named require
	// for the global one.
	WarnTokenScan
)

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

// Warning is a non-fatal issue found while building the graph. For most kinds
// the require was left in place to resolve at runtime rather than being
// embedded; a WarnShadowsBuiltin module is embedded.
type Warning struct {
	Kind   WarningKind
	Module string // the require()d module name (may be empty for dynamic requires).
	File   string // file containing the require ("" for --include packages).
	Line   int
	// Message is a human-readable description; Kind/Module are the machine-
	// readable form. It does not repeat File and Line, which a caller prints
	// alongside.
	Message string
}

// UnresolvedError is returned by Build (in strict mode) when a required module
// cannot be located, unless it is required in protected mode (see
// WarnUnresolved). It wraps resolve.ErrModuleNotFound, so both
// errors.As(&UnresolvedError{}) and errors.Is(resolve.ErrModuleNotFound) work.
type UnresolvedError struct {
	Module string
	File   string // "" for an --include package.
	Line   int
	Err    error
}

func (e *UnresolvedError) Error() string {
	if e.File == "" {
		return fmt.Sprintf("unresolved require %q: %v", e.Module, e.Err)
	}

	return fmt.Sprintf("unresolved require %q at %s:%d: %v", e.Module, e.File, e.Line, e.Err)
}

func (e *UnresolvedError) Unwrap() error { return e.Err }

// ErrNameConflict is the cause Build wraps when one module name is registered
// for two different files, or when the name a package prefix generates is
// the name of a module of the project or of a module some runtime has built
// in. The bundle carries one loader per name, so one module would silently
// replace the other for every caller.
var ErrNameConflict = errors.New("module name conflict")

// Module is a single Lua source file discovered while building the graph,
// along with every require-string that resolves to it.
type Module struct {
	ID       int
	FilePath string // canonical absolute path.
	// Names are all require-strings that map to this file: the names the
	// project's code requires it by and the --include names. Each is a
	// module of its own, as natively. The entry has only the names something
	// requires it by, usually none: the program runs it, no require() loads
	// it.
	Names []string
	// BuiltinNames are the names in Names that some runtime has a built-in
	// module of (see WarnShadowsBuiltin). Where it has one, require loads the
	// runtime's own module and never this file, so the bundle must serve the
	// module under these names only where the runtime finds none of its own.
	BuiltinNames []string
	// Aliases are the names --package-prefix generated from Names. Nothing in
	// the project requires the module by them.
	Aliases []Alias
	// Source is the file's contents as the interpreter compiles them, without
	// the header it skips (see skipHeader): every line keeps its number.
	Source   []byte
	Requires []parse.RequireInfo // parsed require edges.
}

// Alias is a module name generated by --package-prefix. Requiring it loads
// the module under the name it was generated from, so both give the same
// instance.
type Alias struct {
	// Name is the generated name, e.g. "mypkg.util".
	Name string
	// Target is the name it was generated from, e.g. "util".
	Target string
}

// Graph is the full set of Lua modules reachable from an entry file, along
// with any non-fatal warnings collected while resolving them.
type Graph struct {
	Modules []*Module          // all discovered modules.
	ByPath  map[string]*Module // keyed by canonical filepath.
	Entry   *Module
	// EntryName names the entry after its path relative to the root, e.g.
	// "main" for main.lua. It only labels the bundle: nothing registers the
	// entry under it.
	EntryName string
	Warnings  []Warning
}

func moduleNameFromPath(root, filePath string) string {
	if root == "" {
		// Use filename as fallback.
		base := filepath.Base(filePath)
		base = strings.TrimSuffix(base, ".lua")
		base = strings.TrimSuffix(base, ".init")

		return base
	}

	rel, err := filepath.Rel(root, filePath)
	if err != nil || !filepath.IsLocal(rel) {
		// Outside the root (an entry kept apart from the modules), a path
		// from the root would climb out of it with "..": use the filename.
		base := filepath.Base(filePath)
		base = strings.TrimSuffix(base, ".lua")
		base = strings.TrimSuffix(base, ".init")

		return base
	}

	rel = strings.TrimSuffix(rel, ".lua")
	rel = strings.TrimSuffix(rel, ".init")

	rel = strings.ReplaceAll(rel, string(filepath.Separator), ".")
	if rel == "." {
		return "main"
	}

	return rel
}

// applyPackagePrefix gives every name of a module that lacks the package
// prefix an alias with the prefix in front of it. It fails with an error
// wrapping ErrNameConflict when the name it would generate is taken: by a
// module of the project that has that name, or by a module some runtime has
// built in. The bundle registers one module under a name, so the one would
// take the place of the other.
func applyPackagePrefix(g *Graph, packagePrefix string) error {
	if packagePrefix == "" {
		return nil
	}

	owners := make(map[string]*Module)

	for _, mod := range g.Modules {
		for _, name := range mod.Names {
			owners[name] = mod
		}
	}

	for _, mod := range g.Modules {
		for _, name := range mod.Names {
			// A name that carries the prefix already, or whose prefixed
			// form is itself a name of the module, needs no alias.
			prefixed := packagePrefix + "." + name
			if strings.HasPrefix(name, packagePrefix+".") || slices.Contains(mod.Names, prefixed) {
				continue
			}

			err := checkGenerated(packagePrefix, name, prefixed, mod, owners[prefixed])
			if err != nil {
				return err
			}

			mod.Aliases = append(mod.Aliases, Alias{Name: prefixed, Target: name})
		}
	}

	return nil
}

// checkGenerated returns an error wrapping ErrNameConflict when prefixed, the
// name the package prefix generates from name, a name of mod, is taken: by
// owner, the module of the project that has that name, when not nil, or by a
// module some runtime has built in.
func checkGenerated(packagePrefix, name, prefixed string, mod, owner *Module) error {
	turns := fmt.Sprintf("package prefix %q turns %q (%s) into %q", packagePrefix, name, mod.FilePath, prefixed)

	if owner != nil {
		return fmt.Errorf("%w: %s, the name of %s; choose another prefix", ErrNameConflict, turns, owner.FilePath)
	}

	providers := builtinRuntimes(prefixed)
	if providers == 0 {
		return nil
	}

	verb := "have"
	if len(providers.names()) == 1 {
		verb = "has"
	}

	return fmt.Errorf("%w: %s, the name of a module %s %s built in; choose another prefix",
		ErrNameConflict, turns, providers, verb)
}

// requireTarget describes a single require()/--include entry that needs to be
// resolved into the graph.
type requireTarget struct {
	Name string
	File string // "" for --include packages.
	Line int
	// Protected is set for a require called in protected mode, by pcall or
	// xpcall: the program runs on without the module, so not finding it is
	// no error even in strict mode.
	Protected bool
}

func unresolvedWarningMessage(target requireTarget, err error) string {
	switch {
	case target.File == "":
		return fmt.Sprintf("include package %q not found: %v", target.Name, err)
	case target.Protected:
		return fmt.Sprintf("unresolved require %q in protected mode (pcall or xpcall): %v", target.Name, err)
	default:
		return fmt.Sprintf("unresolved require %q: %v", target.Name, err)
	}
}

// addModuleName adds the name target requires mod by to mod's names, once. A
// name that some runtime has a built-in module of is one of mod's
// BuiltinNames too, and the graph warns that those runtimes load their own.
func addModuleName(g *Graph, mod *Module, target requireTarget) {
	if slices.Contains(mod.Names, target.Name) {
		return
	}

	mod.Names = append(mod.Names, target.Name)

	providers := builtinRuntimes(target.Name)
	if providers == 0 {
		return
	}

	mod.BuiltinNames = append(mod.BuiltinNames, target.Name)
	g.Warnings = append(g.Warnings, Warning{
		Kind:    WarnShadowsBuiltin,
		Module:  target.Name,
		File:    target.File,
		Line:    target.Line,
		Message: shadowsBuiltinMessage(target, providers),
	})
}

// shadowsBuiltinMessage describes the WarnShadowsBuiltin warning for target,
// whose name the runtimes in providers have a built-in module of. Like every
// message it leaves the position to the warning's File and Line.
func shadowsBuiltinMessage(target requireTarget, providers runtimes) string {
	where := fmt.Sprintf("require %q", target.Name)
	if target.File == "" {
		where = fmt.Sprintf("include package %q", target.Name)
	}

	verb := "load their"
	if len(providers.names()) == 1 {
		verb = "loads its"
	}

	return fmt.Sprintf("%s: %s %s built-in module %q instead of the project's", where, providers, verb, target.Name)
}

// Resolver locates the file of a require()d module name. A name resolves the
// same wherever it is required from, as with Lua's own require.
// *resolve.Resolver is the implementation.
type Resolver interface {
	Resolve(name string) (*resolve.Result, error)
}

// skipHeader returns source without the header that the interpreter skips
// when it loads a file (luaL_loadfile, see lex.Header): a UTF-8 byte order
// mark, and a first line that starts with "#", such as "#!/usr/bin/env lua".
// The line break that ends that line stays: the interpreter compiles the
// line as an empty one. Nothing else goes, blank lines included, so every
// line keeps its number, in the parser's positions, in warnings and errors,
// and in a --debug traceback. Every file, the entry included, reaches the
// bundle without its header: the bundle is not the file, and a "#!" line in
// the middle of it would not parse.
func skipHeader(source []byte) []byte {
	return source[lex.Header(source):]
}

// resolveTarget resolves a single require()/--include target against the
// graph: it records a warning (or returns an *UnresolvedError in strict mode,
// unless the require is protected) when the target can't be used, dedupes
// against an already-known module, and otherwise reads the source and returns
// a newly created module for the caller to enqueue. created is false whenever
// mod is nil (the target produced only a warning, or deduped into an existing
// module) and needs no further work. A require of a module that some runtime
// has built in and the project has no file for is left to the runtime
// without a word.
func resolveTarget(
	g *Graph, resolver Resolver, cfg *config.Config, target requireTarget,
) (*Module, bool, error) {
	result, err := resolver.Resolve(target.Name)
	if err != nil {
		// The runtimes that have the module provide it, and the others fail
		// to load it natively too, so the bundle has nothing to add. An
		// --include package is a file the user asked for, so it still counts.
		isRequire := target.File != ""
		if isRequire && builtinRuntimes(target.Name) != 0 {
			return nil, false, nil
		}

		if cfg.Strict && !target.Protected {
			return nil, false, &UnresolvedError{Module: target.Name, File: target.File, Line: target.Line, Err: err}
		}

		g.Warnings = append(g.Warnings, Warning{
			Kind:    WarnUnresolved,
			Module:  target.Name,
			File:    target.File,
			Line:    target.Line,
			Message: unresolvedWarningMessage(target, err),
		})

		return nil, false, nil
	}

	ext := strings.ToLower(filepath.Ext(result.FilePath))
	if ext == ".so" || ext == ".dll" {
		g.Warnings = append(g.Warnings, Warning{
			Kind:    WarnCModule,
			Module:  target.Name,
			File:    target.File,
			Line:    target.Line,
			Message: fmt.Sprintf("C module %q cannot be amalgamated", target.Name),
		})

		return nil, false, nil
	}

	if ext != ".lua" {
		g.Warnings = append(g.Warnings, Warning{
			Kind:    WarnNonLua,
			Module:  target.Name,
			File:    target.File,
			Line:    target.Line,
			Message: fmt.Sprintf("non-Lua module %q (extension %s) cannot be amalgamated", target.Name, ext),
		})

		return nil, false, nil
	}

	if existing, ok := g.ByPath[result.FilePath]; ok {
		addModuleName(g, existing, target)
		return nil, false, nil
	}

	// The resolved path comes from resolver.Resolve, which only ever returns
	// paths under the configured root/search directories.
	source, err := os.ReadFile(result.FilePath) // #nosec G304 -- path resolved from configured Lua search roots
	if err != nil {
		return nil, false, fmt.Errorf("read module %s: %w", result.FilePath, err)
	}

	source = skipHeader(source)

	newMod := &Module{
		ID:           len(g.Modules),
		FilePath:     result.FilePath,
		Names:        nil,
		BuiltinNames: nil,
		Aliases:      nil,
		Source:       source,
		Requires:     nil,
	}
	addModuleName(g, newMod, target)
	g.Modules = append(g.Modules, newMod)
	g.ByPath[result.FilePath] = newMod

	return newMod, true, nil
}

func processIncludePackages(
	g *Graph, resolver Resolver, cfg *config.Config, queue []*Module,
) ([]*Module, error) {
	for _, incName := range cfg.IncludePackages {
		if isStandardLibrary(incName) {
			g.Warnings = append(g.Warnings, Warning{
				Kind:   WarnSkipped,
				Module: incName,
				File:   "",
				Line:   0,
				Message: fmt.Sprintf(
					"include package %q skipped: require never loads a standard library module from a file", incName,
				),
			})

			continue
		}

		if cfg.ShouldSkip(incName) {
			g.Warnings = append(g.Warnings, Warning{
				Kind:    WarnSkipped,
				Module:  incName,
				File:    "",
				Line:    0,
				Message: fmt.Sprintf("include package %q skipped (matches skip pattern)", incName),
			})

			continue
		}

		target := requireTarget{Name: incName, File: "", Line: 0, Protected: false}

		newMod, created, err := resolveTarget(g, resolver, cfg, target)
		if err != nil {
			return nil, err
		}

		if created {
			queue = append(queue, newMod)
		}
	}

	return queue, nil
}

func processModuleRequires(
	g *Graph, resolver Resolver, cfg *config.Config, mod *Module, parser parse.Parser,
) ([]*Module, error) {
	found, err := parser.Parse(mod.Source, mod.FilePath)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", mod.FilePath, err)
	}

	if found.Fallback != nil {
		g.Warnings = append(g.Warnings, Warning{
			Kind:   WarnTokenScan,
			Module: "",
			File:   mod.FilePath,
			Line:   found.Fallback.Line,
			Message: "the parser cannot read the file: " + found.Fallback.Reason + "; its requires were found " +
				"by the token scanner, which takes a local named require for the global one",
		})
	}

	requires := found.Requires
	mod.Requires = requires

	var newMods []*Module

	for _, req := range requires {
		if req.Indirect != "" {
			g.Warnings = append(g.Warnings, Warning{
				Kind:    WarnIndirectRequire,
				Module:  "",
				File:    mod.FilePath,
				Line:    req.Line,
				Message: req.Indirect + ", cannot resolve the modules loaded through it",
			})

			continue
		}

		if !req.Static {
			g.Warnings = append(g.Warnings, Warning{
				Kind:    WarnDynamicRequire,
				Module:  req.Name,
				File:    mod.FilePath,
				Line:    req.Line,
				Message: "dynamic require, cannot resolve",
			})

			continue
		}

		// require returns a standard library module from package.loaded
		// without searching, so there is nothing to embed or report.
		if isStandardLibrary(req.Name) {
			continue
		}

		if cfg.ShouldSkip(req.Name) {
			g.Warnings = append(g.Warnings, Warning{
				Kind:    WarnSkipped,
				Module:  req.Name,
				File:    mod.FilePath,
				Line:    req.Line,
				Message: fmt.Sprintf("skipped package %q", req.Name),
			})

			continue
		}

		target := requireTarget{Name: req.Name, File: mod.FilePath, Line: req.Line, Protected: req.Protected}

		newMod, created, err := resolveTarget(g, resolver, cfg, target)
		if err != nil {
			return nil, err
		}

		if created {
			newMods = append(newMods, newMod)
		}
	}

	return newMods, nil
}

// checkNames returns an error wrapping ErrNameConflict when a module name is
// registered for two different files. A name resolves the same wherever it
// is required from, so this guards against a resolver breaking that. Names
// holds only names something requires a file by: the name derived from the
// entry's path (EntryName) registers nothing, so another file may have it.
func checkNames(g *Graph) error {
	owners := make(map[string]*Module)

	for _, mod := range g.Modules {
		for _, name := range mod.Names {
			owner, ok := owners[name]
			if ok && owner != mod {
				return fmt.Errorf("%w: %q is registered for both %s and %s",
					ErrNameConflict, name, owner.FilePath, mod.FilePath)
			}

			owners[name] = mod
		}
	}

	return nil
}

// Build parses cfg.Entry and every file it (transitively) requires into a
// Graph, resolving require() targets via resolver and parsing source via
// parser.
func Build(cfg *config.Config, parser parse.Parser, resolver Resolver) (*Graph, error) {
	err := cfg.ResolveRoot()
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}

	entryPath, err := filepath.Abs(cfg.Entry)
	if err != nil {
		return nil, fmt.Errorf("resolve entry path: %w", err)
	}

	// The entry path comes from the resolved CLI/config Entry setting.
	entrySource, err := os.ReadFile(entryPath) // #nosec G304 -- path resolved from configured Entry setting
	if err != nil {
		return nil, fmt.Errorf("read entry file: %w", err)
	}

	entrySource = skipHeader(entrySource)

	g := &Graph{
		Modules:   nil,
		ByPath:    make(map[string]*Module),
		Entry:     nil,
		EntryName: moduleNameFromPath(cfg.Root, entryPath),
		Warnings:  nil,
	}

	// The entry gets a name only when something requires it (resolveTarget
	// adds it then): the bundle runs the entry as its main chunk.
	entryMod := &Module{
		ID:           0,
		FilePath:     entryPath,
		Names:        nil,
		BuiltinNames: nil,
		Aliases:      nil,
		Source:       entrySource,
		Requires:     nil,
	}
	g.Modules = append(g.Modules, entryMod)
	g.ByPath[entryPath] = entryMod
	g.Entry = entryMod

	queue := []*Module{entryMod}

	// Process included packages.
	queue, err = processIncludePackages(g, resolver, cfg, queue)
	if err != nil {
		return nil, err
	}

	for len(queue) > 0 {
		mod := queue[0]
		queue = queue[1:]

		newMods, err := processModuleRequires(g, resolver, cfg, mod, parser)
		if err != nil {
			return nil, err
		}

		queue = append(queue, newMods...)
	}

	err = checkNames(g)
	if err != nil {
		return nil, err
	}

	err = applyPackagePrefix(g, cfg.PackagePrefix)
	if err != nil {
		return nil, err
	}

	return g, nil
}
