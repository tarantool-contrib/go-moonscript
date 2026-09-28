// Package config loads and represents luamerge's configuration, merging
// built-in defaults, an optional YAML file, and LUAMERGE_-prefixed
// environment variables. Assembly and merging are delegated to the Tarantool
// ecosystem's go-config library rather than a bespoke parser.
package config

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	goconfig "github.com/tarantool/go-config"
	"github.com/tarantool/go-config/collectors"
)

// schemaJSON is the JSON Schema (draft 2020-12) the merged configuration is
// validated against at load time. Environment variables arrive as strings:
// go-config coerces a boolean's value before validation, and envLists splits
// a list's.
//
//go:embed schema.json
var schemaJSON []byte

// errEntryNotSet is returned by ResolveRoot when Root is empty and there is
// no Entry to derive it from.
var errEntryNotSet = errors.New("cannot determine root: entry not set")

// TransformConfig holds the boolean toggles for the Lua source transforms
// applied while merging.
//
//nolint:tagliatelle // config keys are snake_case for YAML/env/CLI consistency, not Go camelCase.
type TransformConfig struct {
	RemoveComments   bool `yaml:"remove_comments"`
	RemoveEmptyLines bool `yaml:"remove_empty_lines"`
	Minify           bool `yaml:"minify"`
}

// Config is the fully resolved luamerge configuration, merged from defaults,
// an optional YAML file, and environment variables. The yaml struct tags name
// the config keys used by go-config for both the YAML file and the LUAMERGE_
// environment variables.
//
//nolint:tagliatelle // config keys are snake_case for YAML/env/CLI consistency, not Go camelCase.
type Config struct {
	Entry           string          `yaml:"entry"`
	Output          string          `yaml:"output"`
	Root            string          `yaml:"root"`
	Path            string          `yaml:"path"`
	Search          []string        `yaml:"search"`
	Strict          bool            `yaml:"strict"`
	Debug           bool            `yaml:"debug"`
	Fallback        bool            `yaml:"fallback"`
	ArgFix          bool            `yaml:"arg_fix"`
	Transform       TransformConfig `yaml:"transform"`
	Prefix          string          `yaml:"prefix"`
	Suffix          string          `yaml:"suffix"`
	Shebang         string          `yaml:"shebang"`
	PackagePrefix   string          `yaml:"package_prefix"`
	PackageName     string          `yaml:"package_name"`
	StripPrefix     string          `yaml:"strip_prefix"`
	SkipPackages    []string        `yaml:"skip_packages"`
	IncludePackages []string        `yaml:"include_packages"`
}

// Default returns the built-in default Config.
func Default() Config {
	return Config{
		Entry:    "",
		Output:   "-",
		Root:     "",
		Path:     "?.lua;?/init.lua",
		Search:   []string{},
		Strict:   false,
		Debug:    false,
		Fallback: false,
		ArgFix:   true,
		Transform: TransformConfig{
			RemoveComments:   false,
			RemoveEmptyLines: false,
			Minify:           false,
		},
		Prefix:          "",
		Suffix:          "",
		Shebang:         "",
		PackagePrefix:   "",
		PackageName:     "",
		StripPrefix:     "",
		SkipPackages:    []string{},
		IncludePackages: []string{},
	}
}

// envPrefix is the prefix of environment variables recognised as configuration
// overrides (e.g. LUAMERGE_STRIP_PREFIX).
const envPrefix = "LUAMERGE_"

// DefaultPath is the configuration file read when no other is named. Unlike a
// file named explicitly, it may be absent.
const DefaultPath = "luamerge.yaml"

// FilePath returns the configuration file LoadConfig(path) reads: path
// itself, or DefaultPath when path is empty.
func FilePath(path string) string {
	return cmp.Or(path, DefaultPath)
}

// SplitList splits the value of a list-valued setting, given on the command
// line or in the environment, into its comma-separated items. Spaces around
// an item are dropped, and so are empty items: "" is the empty list, and
// "lib, vendor," is lib and vendor.
func SplitList(value string) []string {
	items := []string{}

	for item := range strings.SplitSeq(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}

	return items
}

// envKeyToPath maps a LUAMERGE_-prefixed environment variable name (with the
// prefix already stripped by the env collector) to a go-config key path. The
// only nested config section is `transform`, so LUAMERGE_TRANSFORM_X becomes
// transform/x; every other key keeps its underscores intact as a single path
// segment (e.g. LUAMERGE_STRIP_PREFIX -> strip_prefix, LUAMERGE_ARG_FIX ->
// arg_fix). A blanket "_" -> "/" split would mangle every multi-word key.
func envKeyToPath(name string) goconfig.KeyPath {
	name = strings.ToLower(name)
	if rest, ok := strings.CutPrefix(name, "transform_"); ok {
		return goconfig.NewKeyPathFromSegments([]string{"transform", rest})
	}

	return goconfig.NewKeyPath(name)
}

// isList reports whether the setting at path holds a list.
func isList(path goconfig.KeyPath) bool {
	if len(path) != 1 {
		return false
	}

	switch path[0] {
	case "search", "skip_packages", "include_packages":
		return true
	default:
		return false
	}
}

// envLists returns the list-valued settings given in the environment, keyed
// by setting, each value split with SplitList. go-config's environment
// collector passes every value on as one string, which the schema refuses for
// a list.
func envLists() map[string]any {
	lists := make(map[string]any)

	for _, variable := range os.Environ() {
		name, value, ok := strings.Cut(variable, "=")
		if !ok {
			continue
		}

		key, ok := strings.CutPrefix(name, envPrefix)
		if !ok {
			continue
		}

		path := envKeyToPath(key)
		if !isList(path) {
			continue
		}

		items := SplitList(value)

		// A list reaches go-config as []any, like the defaults' lists.
		list := make([]any, len(items))
		for i, item := range items {
			list[i] = item
		}

		lists[path[0]] = list
	}

	return lists
}

// LoadConfig loads the configuration: the built-in defaults, overlaid with the
// YAML file at path and then with LUAMERGE_-prefixed environment variables.
// An empty path means DefaultPath, which is read only when it exists. A file
// named explicitly must exist: a typo in its name is an error matching
// fs.ErrNotExist, not a build without its settings. The three sources are
// assembled and merged by go-config, where a later collector wins over an
// earlier one.
func LoadConfig(path string) (Config, error) {
	configPath := FilePath(path)

	ctx := context.Background()

	builder := goconfig.NewBuilder()

	builder, err := builder.WithJSONSchema(bytes.NewReader(schemaJSON))
	if err != nil {
		return Config{}, fmt.Errorf("load config schema: %w", err)
	}

	builder = builder.AddCollector(collectors.NewStruct(Default()).WithName("defaults"))

	// Only the default file is optional.
	file, ok, err := fileCollector(ctx, configPath, path == "")
	if err != nil {
		return Config{}, err
	}

	if ok {
		builder = builder.AddCollector(file)
	}

	// The lists come after the environment collector, so that a list replaces
	// the string that collector makes of the same variable.
	builder = builder.AddCollector(collectors.NewEnv().WithPrefix(envPrefix).WithTransform(envKeyToPath))
	builder = builder.AddCollector(
		collectors.NewMap(envLists()).WithName("env lists").WithSourceType(goconfig.EnvSource))

	merged, buildErrs := builder.Build(ctx)
	if len(buildErrs) > 0 {
		return Config{}, fmt.Errorf("build config: %w", errors.Join(buildErrs...))
	}

	cfg := Default()

	_, err = merged.Get(goconfig.NewKeyPath(""), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	cfg.ApplyPackageName()

	return cfg, nil
}

// fileCollector returns the collector of the YAML file at configPath. ok is
// false when the file holds no settings to collect: it is optional and does
// not exist, or it is empty. go-config refuses to parse an empty file, while
// one with only comments or blank lines reads as no settings; an empty file,
// such as /dev/null, means the same.
func fileCollector(ctx context.Context, configPath string, optional bool) (goconfig.Collector, bool, error) {
	_, err := os.Stat(configPath)
	if optional && errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("config file: %w", err)
	}

	source, err := collectors.NewSource(ctx, collectors.NewFile(configPath), collectors.NewYamlFormat())
	if errors.Is(err, collectors.ErrNoData) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("load config file %q: %w", configPath, err)
	}

	return source, true, nil
}

// ApplyPackageName implements the package_name convenience: when PackageName is
// set, it fills StripPrefix and PackagePrefix from it unless they are already
// set. Idempotent.
func (c *Config) ApplyPackageName() {
	if c.PackageName == "" {
		return
	}

	if c.StripPrefix == "" {
		c.StripPrefix = c.PackageName
	}

	if c.PackagePrefix == "" {
		c.PackagePrefix = c.PackageName
	}
}

// ResolveRoot derives Root from Entry when Root is not already set.
func (c *Config) ResolveRoot() error {
	if c.Root != "" {
		return nil
	}

	if c.Entry == "" {
		return errEntryNotSet
	}

	absEntry, err := filepath.Abs(c.Entry)
	if err != nil {
		return fmt.Errorf("resolve entry path: %w", err)
	}

	c.Root = filepath.Dir(absEntry)

	return nil
}

// ShouldSkip reports whether name matches a pattern in SkipPackages, honoring
// "pkg.*" prefix wildcards.
func (c *Config) ShouldSkip(name string) bool {
	for _, pattern := range c.SkipPackages {
		if pattern == name {
			return true
		}

		if strings.HasSuffix(pattern, ".*") {
			prefix := pattern[:len(pattern)-2]
			if strings.HasPrefix(name, prefix+".") || name == prefix {
				return true
			}
		}
	}

	return false
}
