// Package emit renders a dependency graph of Lua modules as a single
// amalgamated Lua bundle.
package emit

import (
	"errors"
	"fmt"
	"io"
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

// Options controls how the bundle is rendered.
type Options struct {
	// Prefix is Lua code inserted before the modules.
	Prefix string
	// Suffix is Lua code run after the entry.
	Suffix string
	// Debug emits each module via load(source, "@path") so runtime errors
	// report the original file name and line numbers instead of offsets into
	// the bundle. The source is embedded verbatim (no reindentation).
	Debug bool
	// Fallback registers modules in package.postload behind an appended
	// searcher instead of package.preload, so an on-disk copy on package.path
	// takes precedence and the embedded module is only a fallback.
	Fallback bool
	// Shebang, when non-empty, is written as the first line of the bundle
	// (e.g. "#!/usr/bin/env lua") so the output is directly executable.
	Shebang string
	// NoArgFix omits the `local arg = _G.arg` alias from each module. The alias
	// is emitted by default to counter the implicit `arg` local that Lua 5.1
	// (LUA_COMPAT_VARARG) injects into vararg functions; set this to drop it
	// when targeting an interpreter that doesn't need it (amalg's -a).
	NoArgFix bool
}

// Emit renders the graph g as a single amalgamated Lua bundle to w, applying
// transforms to each module's source in order before it is embedded.
//
// Every module is registered under its names. The entry is not a module: its
// code is the bundle's main chunk. Only when something requires the entry by
// name is it also registered, as a module of its own, like the separate
// instance a require of the entry's file loads natively.
func Emit(w io.Writer, g *graph.Graph, transforms []transform.Transformer, opts Options) error {
	if g.Entry == nil {
		return errNoEntry
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

		data := moduleData{
			PrimaryName: mod.Names[0],
			AliasNames:  mod.Names[1:],
			Source:      source,
			Path:        mod.FilePath,
		}
		moduleDataList = append(moduleDataList, data)
	}

	regTable := "preload"
	if opts.Fallback {
		regTable = "postload"
	}

	data := templateData{
		EntryName: g.EntryName,
		Entry:     entryData{Source: entrySource, Path: g.Entry.FilePath},
		Modules:   moduleDataList,
		Prefix:    prefix,
		Suffix:    suffix,
		Shebang:   strings.TrimRight(opts.Shebang, "\n"),
		Debug:     opts.Debug,
		Fallback:  opts.Fallback,
		NoArgFix:  opts.NoArgFix,
		RegTable:  regTable,
	}

	err = outputTemplate.Execute(w, data)
	if err != nil {
		return fmt.Errorf("execute output template: %w", err)
	}

	return nil
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
