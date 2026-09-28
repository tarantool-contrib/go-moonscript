package luamerge

import (
	"fmt"

	"github.com/tarantool/go-luamerge/internal/config"
)

// Options configures a bundling run. The zero value is not valid — at minimum
// Entry must be set; prefer starting from DefaultOptions. A relative path in
// Entry, Root or Search is taken from the working directory.
type Options struct {
	// Entry is the Lua file the program starts from. Its code runs as the
	// bundle's main chunk. Bundle returns ErrNoEntry when it is empty.
	Entry string
	// Output is where the CLI writes the bundle: a file, or "-" for stdout.
	// Bundle ignores it and writes to the io.Writer it is given; it is here
	// for a caller that takes the output from the configuration, which
	// LoadOptions reads.
	Output string
	// Root is the directory module names are resolved from, as if the
	// program ran from it: a module's name is its path relative to Root.
	// Empty means the entry's directory.
	Root string
	// Path holds the templates a module name is looked up with, separated by
	// ";" as in package.path: a relative template is tried in Root and then
	// in every directory of Search. Empty means "?.lua;?/init.lua", and an
	// empty template in it, from a leading, trailing or doubled ";", stands
	// for those two in its place.
	Path string
	// Search lists the directories searched after Root, in order.
	Search []string
	// Strict makes a require whose module cannot be found fail the build
	// with an *UnresolvedError instead of a WarnUnresolved warning, unless
	// the require is called in protected mode, by pcall or xpcall.
	Strict bool
	// Debug embeds each module, and the entry, as a string run through
	// load(source, "@file"), so a traceback names the original file and line.
	Debug bool
	// Fallback registers the modules in package.postload behind a searcher
	// appended after the interpreter's own, so a module the interpreter finds
	// on disk wins over the embedded one.
	Fallback bool
	// ArgFix emits the arg line, which acts on PUC Lua 5.1 only
	// (LUA_COMPAT_VARARG): there it fills the implicit local arg of every
	// module with the global arg.
	ArgFix bool
	// Transform selects the source transforms applied to every module and
	// to the entry.
	Transform TransformOptions
	// Prefix is Lua code run before the modules are registered, in a block of
	// its own.
	Prefix string
	// Suffix is Lua code run after the entry, in a block of its own.
	Suffix string
	// Shebang, when not empty, is written as the first line of the bundle,
	// such as "#!/usr/bin/env lua".
	Shebang string
	// PackagePrefix makes every module available under its name with this
	// prefix too: with "mypkg", require("mypkg.util") gives the module util.
	PackagePrefix string
	// PackageName is the default of StripPrefix and PackagePrefix: Bundle
	// sets whichever of the two is empty to it.
	PackageName string
	// StripPrefix is stripped from a module name that is found nowhere as it
	// is: "tuple_diff" makes require("tuple_diff.lib.foo") find lib/foo.lua.
	StripPrefix string
	// SkipPackages are the modules left to be required at runtime instead of
	// embedded: an exact name, or "name.*" for a name and every module under
	// it.
	SkipPackages []string
	// IncludePackages are the modules, by exact name, embedded with what they
	// require even when no require of the program reaches them.
	IncludePackages []string
}

// TransformOptions selects the source transforms applied to every module, and
// to the entry, before it is embedded. None is on by default.
type TransformOptions struct {
	// RemoveComments removes every comment. Every line keeps its number.
	RemoveComments bool
	// RemoveEmptyLines removes the lines that hold nothing but white space
	// between tokens.
	RemoveEmptyLines bool
	// Minify removes the comments and the white space between tokens, and
	// takes the place of the other two.
	Minify bool
}

// DefaultOptions returns Options populated with the same defaults the CLI uses
// (stdout output, `?.lua;?/init.lua` path, arg-fix on, all transforms off).
func DefaultOptions() Options {
	return optionsFromConfig(config.Default())
}

// LoadOptions reads Options from a YAML config file and LUAMERGE_* environment
// variables, layered over the defaults. An empty path means luamerge.yaml in
// the working directory, which may be absent: then the defaults plus the
// environment are returned. A file named explicitly must exist; a missing one
// is an error matching fs.ErrNotExist. A setting fills the field of the same
// name, which the file spells in snake case: skip_packages is SkipPackages,
// transform.minify is Transform.Minify. The options are returned as given,
// with the package_name convenience left to Bundle, so any field may still be
// changed.
func LoadOptions(path string) (Options, error) {
	cfg, err := config.LoadConfig(path)
	if err != nil {
		return Options{}, fmt.Errorf("luamerge: load options: %w", err)
	}

	return optionsFromConfig(cfg), nil
}

// optionsFromConfig returns cfg, the configuration as the internal packages
// take it, as Options.
func optionsFromConfig(cfg config.Config) Options {
	return Options{
		Entry:    cfg.Entry,
		Output:   cfg.Output,
		Root:     cfg.Root,
		Path:     cfg.Path,
		Search:   cfg.Search,
		Strict:   cfg.Strict,
		Debug:    cfg.Debug,
		Fallback: cfg.Fallback,
		ArgFix:   cfg.ArgFix,
		Transform: TransformOptions{
			RemoveComments:   cfg.Transform.RemoveComments,
			RemoveEmptyLines: cfg.Transform.RemoveEmptyLines,
			Minify:           cfg.Transform.Minify,
		},
		Prefix:          cfg.Prefix,
		Suffix:          cfg.Suffix,
		Shebang:         cfg.Shebang,
		PackagePrefix:   cfg.PackagePrefix,
		PackageName:     cfg.PackageName,
		StripPrefix:     cfg.StripPrefix,
		SkipPackages:    cfg.SkipPackages,
		IncludePackages: cfg.IncludePackages,
	}
}

// configFromOptions returns opts as the configuration the internal packages
// take.
func configFromOptions(opts Options) config.Config {
	return config.Config{
		Entry:    opts.Entry,
		Output:   opts.Output,
		Root:     opts.Root,
		Path:     opts.Path,
		Search:   opts.Search,
		Strict:   opts.Strict,
		Debug:    opts.Debug,
		Fallback: opts.Fallback,
		ArgFix:   opts.ArgFix,
		Transform: config.TransformConfig{
			RemoveComments:   opts.Transform.RemoveComments,
			RemoveEmptyLines: opts.Transform.RemoveEmptyLines,
			Minify:           opts.Transform.Minify,
		},
		Prefix:          opts.Prefix,
		Suffix:          opts.Suffix,
		Shebang:         opts.Shebang,
		PackagePrefix:   opts.PackagePrefix,
		PackageName:     opts.PackageName,
		StripPrefix:     opts.StripPrefix,
		SkipPackages:    opts.SkipPackages,
		IncludePackages: opts.IncludePackages,
	}
}
