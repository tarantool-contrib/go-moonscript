// Command luamerge bundles a Lua entry point and every module it requires
// (transitively) into a single self-contained Lua file.
package main

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strconv"

	"github.com/spf13/pflag"
	"github.com/tarantool/go-luamerge"
	"github.com/tarantool/go-luamerge/internal/config"
)

// version, commit, and date are injected at build time via -ldflags
// (see .goreleaser.yml), so they must remain package-level vars.
//
//nolint:gochecknoglobals // build-time metadata injected via -ldflags
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		configPath       string
		entry            string
		output           string
		root             string
		path             string
		search           multiString
		skip             multiString
		include          multiString
		strict           optionalBool
		removeComments   optionalBool
		removeEmptyLines optionalBool
		minify           optionalBool
		stripShebang     optionalBool
		debug            optionalBool
		fallback         optionalBool
		noArgFix         optionalBool
		prefix           string
		suffix           string
		shebang          string
		packagePrefix    string
		packageName      string
		stripPrefix      string
		showVersion      bool
	)

	pflag.StringVar(&configPath, "config", "luamerge.yaml", "Path to config file")
	pflag.StringVar(&entry, "entry", "", "Entry Lua file (required)")
	pflag.StringVar(&output, "output", "", "Output file, '-' for stdout (empty = use config or default)")
	pflag.StringVar(&root, "root", "", "Base directory for module resolution")
	pflag.StringVar(&path, "path", "", "Lua path templates, semicolon-separated")
	pflag.Var(&search, "search", "Additional search directory (repeatable)")
	pflag.Var(&skip, "skip", "Skip package (pattern, repeatable)")
	pflag.Var(&include, "include", "Include package (exact name, repeatable)")
	pflag.Var(&strict, "strict", "Treat unresolved requires as errors")
	pflag.Var(&debug, "debug", "Load modules via load(src, '@file') so tracebacks keep original file:line")
	pflag.Var(&fallback, "fallback",
		"Register modules in package.postload behind a searcher so on-disk modules take precedence")
	pflag.Var(&noArgFix, "no-arg-fix",
		"Omit the arg line, which only acts on PUC Lua 5.1 (LUA_COMPAT_VARARG), where it restores the global arg")
	pflag.Var(&removeComments, "remove-comments", "Strip Lua comments from output")
	pflag.Var(&removeEmptyLines, "remove-empty-lines", "Strip empty lines from output")
	pflag.Var(&minify, "minify", "Minify Lua source in output")
	pflag.Var(&stripShebang, "strip-shebang", "Remove shebang line (#!/...) from Lua files")
	pflag.StringVar(&prefix, "prefix", "", "Prefix Lua code run before the modules are registered")
	pflag.StringVar(&suffix, "suffix", "", "Suffix Lua code run after the entry")
	pflag.StringVar(&shebang, "shebang", "",
		"Shebang line written as the first line of the bundle (e.g. '#!/usr/bin/env lua')")
	pflag.StringVar(&packagePrefix, "package-prefix", "",
		"Prefix for all module names (e.g., 'mypkg' makes require('mypkg.module')")
	pflag.StringVar(&packageName, "package-name", "",
		"Package name (sets both strip-prefix and package-prefix to this value)")
	pflag.StringVar(&stripPrefix, "strip-prefix", "",
		"Strip prefix from module names (e.g., 'tuple_diff' makes require('tuple_diff.lib.foo') find 'lib/foo.lua')")
	pflag.BoolVar(&showVersion, "version", false, "Print version and exit")

	// optionalBool flags are custom Var flags, so pflag doesn't infer that they
	// take no argument. Mark each so bare `--flag` means true (otherwise pflag
	// swallows the next token as the flag's value).
	for _, name := range []string{
		"strict", "remove-comments", "remove-empty-lines",
		"minify", "strip-shebang", "debug", "fallback", "no-arg-fix",
	} {
		pflag.Lookup(name).NoOptDefVal = "true"
	}

	pflag.Parse()

	if showVersion {
		_, _ = fmt.Fprintf(os.Stdout, "luamerge version %s (commit %s, built on %s)\n", version, commit, date)
		return 0
	}

	// Load defaults, the YAML config file (if present) and LUAMERGE_ environment
	// variables via go-config.
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// CLI flags override the loaded config, but only when actually set — an
	// unset flag must not clobber a value coming from the file or environment.
	flags := pflag.CommandLine

	stringOverrides := []struct {
		name string
		val  string
		dst  *string
	}{
		{"entry", entry, &cfg.Entry},
		{"output", output, &cfg.Output},
		{"root", root, &cfg.Root},
		{"path", path, &cfg.Path},
		{"prefix", prefix, &cfg.Prefix},
		{"suffix", suffix, &cfg.Suffix},
		{"shebang", shebang, &cfg.Shebang},
		// strip-prefix / package-prefix / package-name must run before the
		// package_name convenience is applied by Bundle.
		{"strip-prefix", stripPrefix, &cfg.StripPrefix},
		{"package-prefix", packagePrefix, &cfg.PackagePrefix},
		{"package-name", packageName, &cfg.PackageName},
	}
	for _, ov := range stringOverrides {
		if flags.Changed(ov.name) {
			*ov.dst = ov.val
		}
	}

	// optionalBool flags are tri-state: apply only when the flag was given.
	boolOverrides := []struct {
		flag optionalBool
		dst  *bool
	}{
		{strict, &cfg.Strict},
		{debug, &cfg.Debug},
		{fallback, &cfg.Fallback},
		{removeComments, &cfg.Transform.RemoveComments},
		{removeEmptyLines, &cfg.Transform.RemoveEmptyLines},
		{minify, &cfg.Transform.Minify},
		{stripShebang, &cfg.Transform.StripShebang},
	}
	for _, ov := range boolOverrides {
		if ov.flag.set {
			*ov.dst = ov.flag.value
		}
	}
	// --no-arg-fix is the CLI inverse of the positive arg_fix config option.
	if noArgFix.set {
		cfg.ArgFix = !noArgFix.value
	}

	// When given on the CLI, list flags replace the config-file values.
	if len(search) > 0 {
		cfg.Search = search
	}

	if len(skip) > 0 {
		cfg.SkipPackages = skip
	}

	if len(include) > 0 {
		cfg.IncludePackages = include
	}

	// Entry is required.
	if cfg.Entry == "" {
		_, _ = fmt.Fprintln(os.Stderr, "error: --entry is required (or set in config file)")
		return 1
	}

	// Render the whole bundle into memory before touching the output (the
	// library Bundle ignores cfg.Output), so that nothing is written unless the
	// build succeeds.
	var bundle bytes.Buffer

	res, err := luamerge.Bundle(cfg, &bundle)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// The config file is as much an input as the sources are.
	err = putOutput(cfg.Output, bundle.Bytes(), slices.Concat(res.Files, []string{configPath}))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	for _, w := range res.Warnings {
		_, _ = fmt.Fprintf(os.Stderr, "warning: %s:%d: %s\n", w.File, w.Line, w.Message)
	}

	return 0
}

type multiString []string

func (m *multiString) String() string {
	return fmt.Sprintf("%v", []string(*m))
}

func (m *multiString) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func (m *multiString) Type() string {
	return "string"
}

type optionalBool struct {
	set   bool
	value bool
}

func (b *optionalBool) Set(s string) error {
	parsed, err := strconv.ParseBool(s)
	if err != nil {
		return fmt.Errorf("parse bool %q: %w", s, err)
	}

	b.set = true
	b.value = parsed

	return nil
}

func (b *optionalBool) String() string {
	if !b.set {
		return ""
	}

	return strconv.FormatBool(b.value)
}

func (b *optionalBool) Type() string {
	return "bool"
}

func (b *optionalBool) IsBoolFlag() bool {
	return true
}
