package luamerge

import (
	"errors"
	"fmt"
	"io"

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
type UnresolvedError struct {
	// Module is the name of the module that cannot be found.
	Module string
	// File is the file of the require, empty for a module of
	// Options.IncludePackages.
	File string
	// Line is the line of the require in File, 0 without a file.
	Line int
	// Err is the cause, which names the files the module was looked for in
	// and wraps ErrModuleNotFound.
	Err error
}

// Error returns the module's name, the position of the require, when there is
// one, and the cause.
func (e *UnresolvedError) Error() string {
	if e.File == "" {
		return fmt.Sprintf("unresolved require %q: %v", e.Module, e.Err)
	}

	return fmt.Sprintf("unresolved require %q at %s:%d: %v", e.Module, e.File, e.Line, e.Err)
}

// Unwrap returns the cause, Err.
func (e *UnresolvedError) Unwrap() error { return e.Err }

// Result is returned by Bundle alongside the written output.
type Result struct {
	// Warnings collected while resolving the dependency graph. Non-fatal; the
	// bundle is still produced. In strict mode an unresolved require is a hard
	// error instead and Bundle returns it, unless the require is called in
	// protected mode, by pcall or xpcall.
	Warnings []Warning
	// Files holds the absolute path of every source file embedded in the
	// bundle: the entry first, then the modules in the order they were
	// discovered. The symbolic links in the entry's path, in the root and in
	// the search directories are resolved, as the names in the bundle are
	// computed from these paths. A caller writing the bundle to disk can use
	// it to refuse an output path that is one of the inputs, or to record the
	// bundle's dependencies. Modules left to be required at runtime (skipped,
	// unresolved, dynamic, C or other non-Lua modules) are not listed.
	Files []string
}

// Bundle amalgamates the project described by opts and writes the resulting Lua
// source to w. It resolves the project root and the package_name convenience
// itself, so a caller only needs to set the fields it cares about. opts is
// taken by value and not mutated.
func Bundle(opts Options, w io.Writer) (*Result, error) {
	if opts.Entry == "" {
		return nil, ErrNoEntry
	}

	cfg := configFromOptions(opts)
	cfg.ApplyPackageName()

	err := cfg.ResolveRoot()
	if err != nil {
		return nil, fmt.Errorf("luamerge: %w", err)
	}

	parser := parse.New()
	resolver := resolve.NewWithPrefix(cfg.Root, cfg.Search, cfg.Path, cfg.StripPrefix)

	g, err := graph.Build(&cfg, parser, resolver)
	if err != nil {
		return nil, fmt.Errorf("luamerge: build graph: %w", unresolvedFromGraph(err))
	}

	transforms := transform.BuildPipeline(cfg.Transform)

	emitOpts := emit.Options{
		Root:       cfg.Root,
		SearchDirs: cfg.Search,
		Prefix:     cfg.Prefix,
		Suffix:     cfg.Suffix,
		Shebang:    cfg.Shebang,
		Debug:      cfg.Debug,
		Fallback:   cfg.Fallback,
		NoArgFix:   !cfg.ArgFix,
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

	return &Result{Warnings: warningsFromGraph(g.Warnings), Files: files}, nil
}

// unresolvedFromGraph returns err, an error of graph.Build, with the
// *graph.UnresolvedError it may be, which Build returns unwrapped, replaced by
// an *UnresolvedError.
func unresolvedFromGraph(err error) error {
	var unresolved *graph.UnresolvedError
	if !errors.As(err, &unresolved) {
		return err
	}

	return &UnresolvedError{
		Module: unresolved.Module,
		File:   unresolved.File,
		Line:   unresolved.Line,
		Err:    unresolved.Err,
	}
}
