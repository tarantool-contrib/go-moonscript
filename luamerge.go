package luamerge

import (
	"errors"
	"fmt"
	"io"

	"github.com/tarantool/go-luamerge/internal/config"
	"github.com/tarantool/go-luamerge/internal/emit"
	"github.com/tarantool/go-luamerge/internal/graph"
	"github.com/tarantool/go-luamerge/internal/parse"
	"github.com/tarantool/go-luamerge/internal/resolve"
	"github.com/tarantool/go-luamerge/internal/transform"
)

// ErrNoEntry is returned by Bundle when Options.Entry is empty.
var ErrNoEntry = errors.New("luamerge: entry not set")

// ErrModuleNotFound is the cause wrapped when a required module cannot be
// located. In strict mode it is wrapped by an *UnresolvedError; check either
// with errors.Is(err, luamerge.ErrModuleNotFound).
var ErrModuleNotFound = resolve.ErrModuleNotFound

// UnresolvedError is returned by Bundle in strict mode when a required module
// cannot be found, unless it is required in protected mode
// (`pcall(require, "x")`, `xpcall(require, h, "x")`), which stays a
// WarnUnresolved warning. Recover it with errors.As, or test the cause with
// errors.Is(err, ErrModuleNotFound).
type UnresolvedError = graph.UnresolvedError

// WarningKind classifies a Warning; see the Warn* constants.
type WarningKind = graph.WarningKind

// Warning kinds, re-exported from the graph package.
const (
	WarnDynamicRequire = graph.WarnDynamicRequire
	WarnSkipped        = graph.WarnSkipped
	WarnUnresolved     = graph.WarnUnresolved
	WarnCModule        = graph.WarnCModule
	WarnNonLua         = graph.WarnNonLua
	// WarnIndirectRequire is require used other than by a call — aliased
	// (`local r = require`), stored or passed on as a value — so the modules
	// loaded through it are not embedded; list them in
	// Options.IncludePackages.
	WarnIndirectRequire = graph.WarnIndirectRequire
)

// Options configures a bundling run. The zero value is not valid — at minimum
// Entry must be set; prefer starting from DefaultOptions. The Output field is
// ignored by Bundle (which writes to the io.Writer it is given) and exists for
// callers that load options from a config file.
type Options = config.Config

// TransformOptions controls source rewriting applied to each module before it
// is embedded (comment and blank-line stripping, and minification).
type TransformOptions = config.TransformConfig

// Warning describes a non-fatal issue found while building the dependency
// graph, such as a dynamic or unresolved require that could not be bundled.
type Warning = graph.Warning

// Result is returned by Bundle alongside the written output.
type Result struct {
	// Warnings collected while resolving the dependency graph. Non-fatal; the
	// bundle is still produced. In strict mode an unresolved require is a hard
	// error instead and Bundle returns it, unless the require is called in
	// protected mode, by pcall or xpcall.
	Warnings []Warning
	// Files holds the absolute path of every source file embedded in the
	// bundle: the entry first, then the modules in the order they were
	// discovered. The symbolic links in the entry's path and in the root are
	// resolved, as module names are computed from them. A caller writing the
	// bundle to disk can use it to refuse an output path that is one of the
	// inputs, or to record the bundle's dependencies. Modules left to be
	// required at runtime (skipped, unresolved, dynamic, C or other non-Lua
	// modules) are not listed.
	Files []string
}

// DefaultOptions returns Options populated with the same defaults the CLI uses
// (stdout output, `?.lua;?/init.lua` path, arg-fix on, all transforms off).
func DefaultOptions() Options {
	return config.Default()
}

// LoadOptions reads Options from a YAML config file and LUAMERGE_* environment
// variables, layered over the defaults. An empty path means luamerge.yaml in
// the working directory, which may be absent: then the defaults plus the
// environment are returned. A file named explicitly must exist; a missing one
// is an error matching fs.ErrNotExist. The options are returned as given, with
// the package_name convenience left to Bundle, so any field may still be
// changed.
func LoadOptions(path string) (Options, error) {
	cfg, err := config.LoadConfig(path)
	if err != nil {
		return Options{}, fmt.Errorf("luamerge: load options: %w", err)
	}

	return cfg, nil
}

// Bundle amalgamates the project described by opts and writes the resulting Lua
// source to w. It resolves the project root and the package_name convenience
// itself, so a caller only needs to set the fields it cares about. opts is
// taken by value and not mutated.
func Bundle(opts Options, w io.Writer) (*Result, error) {
	if opts.Entry == "" {
		return nil, ErrNoEntry
	}

	opts.ApplyPackageName()

	err := opts.ResolveRoot()
	if err != nil {
		return nil, fmt.Errorf("luamerge: %w", err)
	}

	parser := parse.New()
	resolver := resolve.NewWithPrefix(opts.Root, opts.Search, opts.Path, opts.StripPrefix)

	g, err := graph.Build(&opts, parser, resolver)
	if err != nil {
		return nil, fmt.Errorf("luamerge: build graph: %w", err)
	}

	transforms := transform.BuildPipeline(opts.Transform)

	emitOpts := emit.Options{
		Prefix:   opts.Prefix,
		Suffix:   opts.Suffix,
		Shebang:  opts.Shebang,
		Debug:    opts.Debug,
		Fallback: opts.Fallback,
		NoArgFix: !opts.ArgFix,
	}

	err = emit.Emit(w, g, transforms, emitOpts)
	if err != nil {
		return nil, fmt.Errorf("luamerge: emit: %w", err)
	}

	// g.Modules starts with the entry and grows in discovery order.
	files := make([]string, len(g.Modules))
	for i, mod := range g.Modules {
		files[i] = mod.FilePath
	}

	return &Result{Warnings: g.Warnings, Files: files}, nil
}
