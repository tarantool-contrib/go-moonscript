// Package emit renders a dependency graph of Lua modules as a single
// amalgamated Lua bundle.
package emit

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/transform"
)

// errNoEntry is returned by Emit for a graph without an entry.
var errNoEntry = errors.New("the graph has no entry")

// errNoName is returned by Emit for a module other than the entry that has no
// name to register it under.
var errNoName = errors.New("the module has no name")

// errNotAbsolute is the cause Emit wraps when Options.Root or a directory of
// Options.SearchDirs is not an absolute path.
var errNotAbsolute = errors.New("not an absolute path")

// Options controls how the bundle is rendered.
type Options struct {
	// Root is the directory the program is meant to run from, the project
	// root, as an absolute path; Emit requires it. In debug mode a chunk is
	// named after its file's path relative to it (see SearchDirs).
	Root string
	// SearchDirs are the directories modules are searched in besides Root,
	// as absolute paths with their symbolic links resolved, as Root's are.
	// In debug mode a module in one of them, outside Root, is named after
	// its path relative to Root, and a module outside all of them, which
	// only an absolute template of the search path reaches, after its
	// absolute path (see moduleChunkName).
	SearchDirs []string
	// Prefix is Lua code run before the modules are registered, in a block of
	// its own.
	Prefix string
	// Suffix is Lua code run after the entry, in a block of its own.
	Suffix string
	// Debug emits each module, and the entry, via load(source, "@path"), path
	// as the interpreter names the file when the program runs from Root, so
	// runtime errors report the file name and line numbers the original
	// program reports instead of offsets into the bundle. The source is
	// embedded verbatim (no reindentation).
	Debug bool
	// Fallback registers modules in package.postload behind an appended
	// searcher instead of package.preload, so an on-disk copy on package.path
	// takes precedence and the embedded module is only a fallback.
	Fallback bool
	// Shebang, when non-empty, is written as the first line of the bundle
	// (e.g. "#!/usr/bin/env lua") so the output is directly executable.
	Shebang string
	// NoArgFix omits the arg line from the function around each module body
	// (and around the entry when there is a suffix). PUC Lua 5.1 with
	// LUA_COMPAT_VARARG gives every vararg function an implicit local `arg`,
	// which hides the global `arg` from the body; the line fills that local
	// with the global table and touches nothing on any other interpreter. Set
	// this to drop it (amalg's -a).
	NoArgFix bool
}

// Emit renders the graph g as a single amalgamated Lua bundle to w, applying
// transforms to each module's source in order before it is embedded.
//
// Every module is registered under each of its names with the same loader,
// so that each name is an instance of its own, as natively. A name that some
// runtime has a built-in module of is served right after the interpreter's
// first searcher instead, so the runtime's module wins where it has one and
// the embedded one wins over files on disk everywhere else. A name a package
// prefix made of such a name (a Deferral) gets a loader of its own, which
// gives the runtime's module where there is one. The entry is not a module:
// its code is the bundle's main chunk. Only when something requires the entry
// by name is it also registered, as a module of its own, like the separate
// instance a require of the entry's file loads natively.
func Emit(w io.Writer, g *graph.Graph, transforms []transform.Transformer, opts Options) error {
	if g.Entry == nil {
		return errNoEntry
	}

	// A relative root or search directory would leave the chunk names of the
	// files in it their absolute paths.
	if !filepath.IsAbs(opts.Root) {
		return fmt.Errorf("root %q: %w", opts.Root, errNotAbsolute)
	}

	for _, dir := range opts.SearchDirs {
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("search directory %q: %w", dir, errNotAbsolute)
		}
	}

	prefix, suffix := opts.Prefix, opts.Suffix
	// Ensure prefix ends with newline if present.
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}

	// Ensure suffix ends with newline if present.
	if suffix != "" && !strings.HasSuffix(suffix, "\n") {
		suffix += "\n"
	}

	entrySource, err := transformSource(g.Entry, transforms)
	if err != nil {
		return err
	}

	// Collect the modules to register, sorted by primary name. The caller's
	// graph is not reordered.
	modules := make([]*graph.Module, 0, len(g.Modules))

	for _, mod := range g.Modules {
		if len(mod.Names) > 0 {
			modules = append(modules, mod)
		} else if mod != g.Entry {
			return fmt.Errorf("emit %s: %w", mod.FilePath, errNoName)
		}
	}

	sort.Slice(modules, func(i, j int) bool { return modules[i].Names[0] < modules[j].Names[0] })

	// Build module data for the template.
	moduleDataList := make([]moduleData, 0, len(modules))

	for _, mod := range modules {
		source := entrySource
		if mod != g.Entry {
			source, err = transformSource(mod, transforms)
			if err != nil {
				return err
			}
		}

		names, builtinNames := splitNames(mod, opts.Fallback)

		chunk := moduleChunkName(opts.Root, opts.SearchDirs, mod.FilePath)
		data := moduleData{
			Names:        names,
			BuiltinNames: builtinNames,
			Deferrals:    mod.Deferrals,
			Code:         codeData{Source: source, Chunk: chunk, ArgFix: !opts.NoArgFix},
		}
		moduleDataList = append(moduleDataList, data)
	}

	regTable := "preload"
	if opts.Fallback {
		regTable = "postload"
	}

	entryChunk := entryChunkName(opts.Root, g.Entry.FilePath)

	data := templateData{
		EntryName: g.EntryName,
		Entry:     codeData{Source: entrySource, Chunk: entryChunk, ArgFix: !opts.NoArgFix},
		Modules:   moduleDataList,
		Prefix:    prefix,
		Suffix:    suffix,
		Shebang:   strings.TrimRight(opts.Shebang, "\n"),
		Debug:     opts.Debug,
		Fallback:  opts.Fallback,
		RegTable:  regTable,
	}

	err = outputTemplate.Execute(w, data)
	if err != nil {
		return fmt.Errorf("execute output template: %w", err)
	}

	return nil
}

// splitNames splits the names of mod, in their order, into the names the
// bundle registers with the module's loader and the names some runtime has a
// built-in module of (the module's BuiltinNames), which the bundle serves
// right after the interpreter's first searcher. In fallback mode every such
// name goes behind the appended searcher with the others. The names of the
// module's Deferrals are in neither: they have loaders of their own.
func splitNames(mod *graph.Module, fallback bool) ([]string, []string) {
	var names, builtinNames []string

	for _, name := range mod.Names {
		switch {
		case slices.ContainsFunc(mod.Deferrals, func(d graph.Deferral) bool { return d.Name == name }):
		case !fallback && slices.Contains(mod.BuiltinNames, name):
			builtinNames = append(builtinNames, name)
		default:
			names = append(names, name)
		}
	}

	return names, builtinNames
}

// transformSource returns the source of mod with transforms applied in order.
func transformSource(mod *graph.Module, transforms []transform.Transformer) (string, error) {
	source := mod.Source
	for _, t := range transforms {
		transformed, err := t.Transform(source)
		if err != nil {
			return "", fmt.Errorf("transform %s: %w", mod.FilePath, err)
		}

		source = transformed
	}

	return string(source), nil
}
