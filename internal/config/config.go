// Package config loads and represents luamerge's configuration, merging
// built-in defaults, an optional YAML file, and LUAMERGE_-prefixed
// environment variables. Assembly and merging are delegated to the Tarantool
// ecosystem's go-config library rather than a bespoke parser.
package config

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	goconfig "github.com/tarantool/go-config"
	"github.com/tarantool/go-config/collectors"
)

// schemaJSON is the JSON Schema (draft 2020-12) the merged configuration is
// validated against at load time. Boolean and list fields also accept a string
// form because environment-variable overrides arrive as strings before the
// typed decode.
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
	StripShebang     bool `yaml:"strip_shebang"`
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
			StripShebang:     false,
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

// LoadConfig loads configuration from configPath (defaulting to
// "luamerge.yaml" when empty), overlaying built-in defaults with the YAML
// file (if it exists) and then LUAMERGE_-prefixed environment variables. The
// three sources are assembled and merged by go-config, where a later collector
// wins over an earlier one.
func LoadConfig(configPath string) (Config, error) {
	if configPath == "" {
		configPath = "luamerge.yaml"
	}

	ctx := context.Background()

	builder := goconfig.NewBuilder()

	builder, err := builder.WithJSONSchema(bytes.NewReader(schemaJSON))
	if err != nil {
		return Config{}, fmt.Errorf("load config schema: %w", err)
	}

	builder = builder.AddCollector(collectors.NewStruct(Default()).WithName("defaults"))

	// Layer the YAML file only when it exists; a missing file is not an error.
	_, statErr := os.Stat(configPath)

	switch {
	case statErr == nil:
		source, srcErr := collectors.NewSource(ctx, collectors.NewFile(configPath), collectors.NewYamlFormat())
		if srcErr != nil {
			return Config{}, fmt.Errorf("load config file %q: %w", configPath, srcErr)
		}

		builder = builder.AddCollector(source)
	case !os.IsNotExist(statErr):
		return Config{}, fmt.Errorf("stat config file %q: %w", configPath, statErr)
	}

	builder = builder.AddCollector(collectors.NewEnv().WithPrefix(envPrefix).WithTransform(envKeyToPath))

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
