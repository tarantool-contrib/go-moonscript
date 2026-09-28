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
	"github.com/tarantool/go-luamerge/internal/parse"
	"github.com/tarantool/go-luamerge/internal/resolve"
	"github.com/tarantool/go-luamerge/internal/transform"
)

// namesPrefixCapFactor is the growth factor used when pre-sizing a module's
// Names slice to hold both original and package-prefixed names.
const namesPrefixCapFactor = 2

// WarningKind classifies a non-fatal issue found while building the graph.
type WarningKind int

const (
	// WarnDynamicRequire is a require() with a non-literal argument that can't
	// be resolved statically.
	WarnDynamicRequire WarningKind = iota
	// WarnSkipped is a require() excluded by a skip pattern.
	WarnSkipped
	// WarnUnresolved is a require() whose module could not be found (non-strict
	// mode; strict mode returns an *UnresolvedError instead).
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
	default:
		return "unknown"
	}
}

// Warning is a non-fatal issue found while building the graph. The require was
// left in place to resolve at runtime rather than being embedded.
type Warning struct {
	Kind   WarningKind
	Module string // the require()d module name (may be empty for dynamic requires).
	File   string // file containing the require ("" for --include packages).
	Line   int
	// Message is a human-readable description; Kind/Module are the machine-
	// readable form.
	Message string
}

// UnresolvedError is returned by Build (in strict mode) when a required module
// cannot be located. It wraps resolve.ErrModuleNotFound, so both
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
// for two different files. The bundle carries one loader per name, so one
// file would silently replace the other for every caller.
var ErrNameConflict = errors.New("module name conflict")

// Module is a single Lua source file discovered while building the graph,
// along with every require-string that resolves to it.
type Module struct {
	ID       int
	FilePath string              // canonical absolute path.
	Names    []string            // all require-strings that map to this file.
	Source   []byte              // raw file contents.
	Requires []parse.RequireInfo // parsed require edges.
}

// Graph is the full set of Lua modules reachable from an entry file, along
// with any non-fatal warnings collected while resolving them.
type Graph struct {
	Modules  []*Module          // all discovered modules.
	ByPath   map[string]*Module // keyed by canonical filepath.
	Entry    *Module
	Warnings []Warning
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
	if err != nil {
		// If relative path cannot be computed, use filename.
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

func applyPackagePrefix(g *Graph, packagePrefix string) {
	if packagePrefix == "" {
		return
	}

	for _, mod := range g.Modules {
		// Create prefixed versions of all names, avoiding duplicates.
		prefixedNames := make([]string, 0, len(mod.Names)*namesPrefixCapFactor)
		// Keep original names first for internal requires.
		prefixedNames = append(prefixedNames, mod.Names...)
		// Track existing names for deduplication.
		existing := make(map[string]bool, len(mod.Names))
		for _, name := range mod.Names {
			existing[name] = true
		}

		for _, name := range mod.Names {
			// Don't add prefix if name already starts with it.
			if strings.HasPrefix(name, packagePrefix+".") {
				continue
			}

			prefixed := packagePrefix + "." + name
			if !existing[prefixed] {
				prefixedNames = append(prefixedNames, prefixed)
				existing[prefixed] = true
			}
		}

		mod.Names = prefixedNames
	}
	// For entry module, ensure a prefixed name is first so final require uses it.
	if g.Entry != nil && len(g.Entry.Names) > 0 {
		// Find a prefixed name.
		for i, name := range g.Entry.Names {
			if strings.HasPrefix(name, packagePrefix+".") {
				// Move this prefixed name to the front.
				if i != 0 {
					g.Entry.Names = append([]string{name}, append(g.Entry.Names[:i], g.Entry.Names[i+1:]...)...)
				}

				break
			}
		}
	}
}

// requireTarget describes a single require()/--include entry that needs to be
// resolved into the graph.
type requireTarget struct {
	Name string
	File string // "" for --include packages.
	Line int
}

func unresolvedWarningMessage(target requireTarget, err error) string {
	if target.File == "" {
		return fmt.Sprintf("include package %q not found: %v", target.Name, err)
	}

	return fmt.Sprintf("unresolved require %q at line %d: %v", target.Name, target.Line, err)
}

func addModuleName(mod *Module, name string) {
	if !slices.Contains(mod.Names, name) {
		mod.Names = append(mod.Names, name)
	}
}

// Resolver locates the file of a require()d module name. A name resolves the
// same wherever it is required from, as with Lua's own require.
// *resolve.Resolver is the implementation.
type Resolver interface {
	Resolve(name string) (*resolve.Result, error)
}

// resolveTarget resolves a single require()/--include target against the
// graph: it records a warning (or returns an *UnresolvedError in strict mode)
// when the target can't be used, dedupes against an already-known module, and
// otherwise reads the source and returns a newly created module for the
// caller to enqueue. created is false whenever mod is nil (the target
// produced only a warning, or deduped into an existing module) and needs no
// further work.
func resolveTarget(
	g *Graph, resolver Resolver, cfg *config.Config, target requireTarget,
) (*Module, bool, error) {
	result, err := resolver.Resolve(target.Name)
	if err != nil {
		if cfg.Strict {
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
		addModuleName(existing, target.Name)
		return nil, false, nil
	}

	// The resolved path comes from resolver.Resolve, which only ever returns
	// paths under the configured root/search directories.
	source, err := os.ReadFile(result.FilePath) // #nosec G304 -- path resolved from configured Lua search roots
	if err != nil {
		return nil, false, fmt.Errorf("read module %s: %w", result.FilePath, err)
	}

	source = transform.StripShebang(source)

	newMod := &Module{
		ID:       len(g.Modules),
		FilePath: result.FilePath,
		Names:    []string{target.Name},
		Source:   source,
		Requires: nil,
	}
	g.Modules = append(g.Modules, newMod)
	g.ByPath[result.FilePath] = newMod

	return newMod, true, nil
}

func processIncludePackages(
	g *Graph, resolver Resolver, cfg *config.Config, queue []*Module,
) ([]*Module, error) {
	for _, incName := range cfg.IncludePackages {
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

		target := requireTarget{Name: incName, File: "", Line: 0}

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
	requires, err := parser.Parse(mod.Source, mod.FilePath)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", mod.FilePath, err)
	}

	mod.Requires = requires

	var newMods []*Module

	for _, req := range requires {
		if req.Indirect != "" {
			g.Warnings = append(g.Warnings, Warning{
				Kind:   WarnIndirectRequire,
				Module: "",
				File:   mod.FilePath,
				Line:   req.Line,
				Message: fmt.Sprintf(
					"%s at line %d, cannot resolve the modules loaded through it", req.Indirect, req.Line,
				),
			})

			continue
		}

		if !req.Static {
			g.Warnings = append(g.Warnings, Warning{
				Kind:    WarnDynamicRequire,
				Module:  req.Name,
				File:    mod.FilePath,
				Line:    req.Line,
				Message: fmt.Sprintf("dynamic require at line %d, cannot resolve", req.Line),
			})

			continue
		}

		if cfg.ShouldSkip(req.Name) {
			g.Warnings = append(g.Warnings, Warning{
				Kind:    WarnSkipped,
				Module:  req.Name,
				File:    mod.FilePath,
				Line:    req.Line,
				Message: fmt.Sprintf("skipped package %q at line %d", req.Name, req.Line),
			})

			continue
		}

		target := requireTarget{Name: req.Name, File: mod.FilePath, Line: req.Line}

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
// is required from, so this guards against a resolver breaking that, and
// against a name given to the entry by its path that another file resolves
// to.
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

	entrySource = transform.StripShebang(entrySource)

	g := &Graph{
		Modules:  nil,
		ByPath:   make(map[string]*Module),
		Entry:    nil,
		Warnings: nil,
	}

	entryMod := &Module{
		ID:       0,
		FilePath: entryPath,
		Names:    []string{moduleNameFromPath(cfg.Root, entryPath)},
		Source:   entrySource,
		Requires: nil,
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

	applyPackagePrefix(g, cfg.PackagePrefix)

	return g, nil
}
