// Command luamerge bundles a Lua entry point and every module it requires
// (transitively) into a single self-contained Lua file.
package main

import (
	"bytes"
	"cmp"
	"fmt"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
	"github.com/tarantool/go-luamerge"
	"github.com/tarantool/go-luamerge/internal/config"
)

// version, commit, and date are injected at build time via -ldflags
// (see .goreleaser.yml), so they must remain package-level vars. Left empty,
// they are taken from the build information (see versionLine).
//
//nolint:gochecknoglobals // build-time metadata injected via -ldflags
var (
	version = ""
	commit  = ""
	date    = ""
)

// exitUsage is the exit status of a mistake on the command line, the one
// pflag exits with for an unknown flag.
const exitUsage = 2

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

	pflag.StringVar(&configPath, "config", config.DefaultPath,
		"Config file; a file named here must exist, the default is read only if present")
	pflag.StringVar(&entry, "entry", "", "Entry Lua file (required)")
	pflag.StringVar(&output, "output", "", "Output file, '-' for stdout (empty = use config or default)")
	pflag.StringVar(&root, "root", "", "Base directory for module resolution")
	pflag.StringVar(&path, "path", "", "Lua path templates, semicolon-separated")
	pflag.Var(&search, "search", "Additional search directories, relative to the working directory, not the root "+
		"(comma-separated or repeated; replaces a configured list)")
	pflag.Var(&skip, "skip", "Skip packages (patterns, comma-separated or repeated; replaces a configured list)")
	pflag.Var(&include, "include",
		"Include packages (exact names, comma-separated or repeated; replaces a configured list)")
	pflag.Var(&strict, "strict",
		"Treat unresolved requires as errors; those in pcall or xpcall stay warnings")
	pflag.Var(&debug, "debug", "Load modules via load(src, '@file') so tracebacks keep original file:line")
	pflag.Var(&fallback, "fallback",
		"Register modules in package.postload behind a searcher so on-disk modules take precedence")
	pflag.Var(&noArgFix, "no-arg-fix",
		"Omit the arg line, which only acts on PUC Lua 5.1 (LUA_COMPAT_VARARG), where it restores the global arg")
	pflag.Var(&removeComments, "remove-comments", "Strip Lua comments from output")
	pflag.Var(&removeEmptyLines, "remove-empty-lines", "Strip empty lines from output")
	pflag.Var(&minify, "minify", "Minify Lua source in output")
	pflag.StringVar(&prefix, "prefix", "", "Prefix Lua code run before the modules are registered")
	pflag.StringVar(&suffix, "suffix", "", "Suffix Lua code run after the entry")
	pflag.StringVar(&shebang, "shebang", "",
		"Shebang line written as the first line of the bundle (e.g. '#!/usr/bin/env lua')")
	pflag.StringVar(&packagePrefix, "package-prefix", "",
		"Prefix for all module names (e.g., 'mypkg' makes require('mypkg.module')")
	pflag.StringVar(&packageName, "package-name", "",
		"Package name (strip-prefix and package-prefix take this value unless set themselves)")
	pflag.StringVar(&stripPrefix, "strip-prefix", "",
		"Strip prefix from module names (e.g., 'tuple_diff' makes require('tuple_diff.lib.foo') find 'lib/foo.lua')")
	pflag.BoolVar(&showVersion, "version", false, "Print version and exit")

	// optionalBool flags are custom Var flags, so pflag doesn't infer that they
	// take no argument. Mark each so bare `--flag` means true (otherwise pflag
	// swallows the next token as the flag's value).
	for _, name := range []string{
		"strict", "remove-comments", "remove-empty-lines",
		"minify", "debug", "fallback", "no-arg-fix",
	} {
		pflag.Lookup(name).NoOptDefVal = "true"
	}

	// Parsing stops at the first positional argument, so the argument before
	// it is the last one parsed (see unexpectedArgument).
	pflag.CommandLine.SetInterspersed(false)
	pflag.Parse()

	if pflag.NArg() > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "error: %s\n", unexpectedArgument(pflag.CommandLine, os.Args[1:]))
		return exitUsage
	}

	if showVersion {
		_, _ = fmt.Fprintln(os.Stdout, versionLine(version, commit, date, ownBuildInfo()))

		return 0
	}

	flags := pflag.CommandLine

	// Only a file named with --config must exist. Without the flag, or with
	// an empty value, the default file is read if it is there. configPath is
	// what LoadOptions takes, and config.FilePath(configPath) the file it
	// reads.
	if !flags.Changed("config") {
		configPath = ""
	}

	// Load defaults, the YAML config file and LUAMERGE_ environment variables.
	opts, err := luamerge.LoadOptions(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// CLI flags override the loaded config, but only when actually set — an
	// unset flag must not clobber a value coming from the file or environment.
	stringOverrides := []struct {
		name string
		val  string
		dst  *string
	}{
		{"entry", entry, &opts.Entry},
		{"root", root, &opts.Root},
		{"path", path, &opts.Path},
		{"prefix", prefix, &opts.Prefix},
		{"suffix", suffix, &opts.Suffix},
		{"shebang", shebang, &opts.Shebang},
		// strip-prefix / package-prefix / package-name must run before the
		// package_name convenience is applied by Bundle.
		{"strip-prefix", stripPrefix, &opts.StripPrefix},
		{"package-prefix", packagePrefix, &opts.PackagePrefix},
		{"package-name", packageName, &opts.PackageName},
	}
	for _, ov := range stringOverrides {
		if flags.Changed(ov.name) {
			*ov.dst = ov.val
		}
	}

	// An empty --output leaves the output to the configuration, whose default
	// is stdout, as the flag's help says; a file named "" does not exist.
	if flags.Changed("output") && output != "" {
		opts.Output = output
	}

	// optionalBool flags are tri-state: apply only when the flag was given.
	boolOverrides := []struct {
		flag optionalBool
		dst  *bool
	}{
		{strict, &opts.Strict},
		{debug, &opts.Debug},
		{fallback, &opts.Fallback},
		{removeComments, &opts.Transform.RemoveComments},
		{removeEmptyLines, &opts.Transform.RemoveEmptyLines},
		{minify, &opts.Transform.Minify},
	}
	for _, ov := range boolOverrides {
		if ov.flag.set {
			*ov.dst = ov.flag.value
		}
	}
	// --no-arg-fix is the CLI inverse of the positive arg_fix config option.
	if noArgFix.set {
		opts.ArgFix = !noArgFix.value
	}

	// A list flag replaces the configured list, as every flag overrides its
	// setting; given with no items, it clears the list.
	listOverrides := []struct {
		name string
		val  multiString
		dst  *[]string
	}{
		{"search", search, &opts.Search},
		{"skip", skip, &opts.SkipPackages},
		{"include", include, &opts.IncludePackages},
	}
	for _, ov := range listOverrides {
		if flags.Changed(ov.name) {
			*ov.dst = ov.val
		}
	}

	// Entry is required.
	if opts.Entry == "" {
		_, _ = fmt.Fprintln(os.Stderr, "error: --entry is required (or set in config file)")
		return 1
	}

	// Render the whole bundle into memory before touching the output (the
	// library Bundle ignores opts.Output), so that nothing is written unless
	// the build succeeds.
	var bundle bytes.Buffer

	res, err := luamerge.Bundle(opts, &bundle)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// The config file is as much an input as the sources are.
	err = putOutput(opts.Output, bundle.Bytes(), slices.Concat(res.Files, []string{config.FilePath(configPath)}))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	for _, w := range res.Warnings {
		_, _ = fmt.Fprintf(os.Stderr, "warning: %s\n", warningText(w))
	}

	return 0
}

// warningText is w as the CLI prints it: its position, when it has one, then
// its message, which does not repeat the position. A warning about an
// --include package has no file.
func warningText(w luamerge.Warning) string {
	switch {
	case w.File == "":
		return w.Message
	case w.Line <= 0:
		return w.File + ": " + w.Message
	default:
		return fmt.Sprintf("%s:%d: %s", w.File, w.Line, w.Message)
	}
}

// ownBuildInfo returns the build information of the running binary, or nil
// when it has none.
func ownBuildInfo() *debug.BuildInfo {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}

	return info
}

// versionLine is what --version prints. GoReleaser sets version, commit and
// the build date with -ldflags. Whatever it did not set is taken from info,
// the build information the Go toolchain records in a binary (nil if there
// is none): the module version, which go install module@version records,
// and for a build in a git checkout the commit's revision and time. What is
// known of neither is left out.
func versionLine(version, commit, date string, info *debug.BuildInfo) string {
	dateLabel := "built on"

	if info != nil {
		if version == "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}

		for _, setting := range info.Settings {
			switch {
			case setting.Key == "vcs.revision" && commit == "":
				commit = setting.Value
			case setting.Key == "vcs.time" && date == "":
				date = setting.Value
				dateLabel = "committed on"
			}
		}
	}

	var details []string

	if commit != "" {
		details = append(details, "commit "+commit)
	}

	if date != "" {
		details = append(details, dateLabel+" "+date)
	}

	line := "luamerge version " + cmp.Or(version, "dev")
	if len(details) > 0 {
		line += " (" + strings.Join(details, ", ") + ")"
	}

	return line
}

// unexpectedArgument describes the first positional argument of args, the
// arguments parsed into flags, for an error message; luamerge takes none.
// Parsing stopped at that argument, so the one before it, if any, was the
// last parsed. When that is a flag, it is one that does not take the next
// argument as its value, since it would have taken this one: a boolean flag,
// which takes a value only after "=". If the stray word reads as a boolean,
// it was meant as that value (--minify false turns minification on and
// leaves "false" over), and the message shows the "=" form.
func unexpectedArgument(flags *pflag.FlagSet, args []string) string {
	i := len(args) - flags.NArg()
	stray := args[i]

	msg := fmt.Sprintf("unexpected argument %q: luamerge takes no positional arguments", stray)

	if strings.HasSuffix(stray, ".lua") {
		return msg + "; name the entry with --entry " + stray
	}

	if i == 0 {
		return msg
	}

	// The "--" that ends the flags is no flag.
	name, isFlag := strings.CutPrefix(args[i-1], "--")
	_, err := strconv.ParseBool(stray)

	if isFlag && flags.Lookup(name) != nil && err == nil {
		return fmt.Sprintf("%s; a boolean flag takes its value after \"=\": --%s=%s", msg, name, stray)
	}

	return msg
}

// multiString is a list flag. A value holds comma-separated items, split as
// in the environment (see config.SplitList), and a repeated flag adds its
// items to the list.
type multiString []string

func (m *multiString) String() string {
	return strings.Join(*m, ",")
}

func (m *multiString) Set(value string) error {
	*m = append(*m, config.SplitList(value)...)
	return nil
}

func (m *multiString) Type() string {
	return "strings"
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
