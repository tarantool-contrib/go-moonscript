// Package resolve implements Lua module name resolution against a
// configurable search path, mirroring Lua's require() semantics.
package resolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrModuleNotFound is the cause wrapped by Resolve when a module cannot be
// located in any search path. Check it with errors.Is.
var ErrModuleNotFound = errors.New("module not found")

// Resolver locates Lua source files for require()-style module names by
// searching a root directory, a set of additional directories, and a list
// of path templates.
type Resolver struct {
	Root        string   // base directory.
	SearchDirs  []string // additional directories to search.
	Templates   []string // e.g. ["?.lua", "?/init.lua"].
	StripPrefix string   // if set, strip this prefix from module names (e.g., "tuple_diff").
}

// Result is the outcome of a successful module resolution.
type Result struct {
	FilePath string // absolute normalized path to .lua file.
	ModName  string // canonical module name (dots notation).
}

// New creates a Resolver with no package-prefix stripping.
func New(root string, searchDirs []string, pathTemplate string) *Resolver {
	return NewWithPrefix(root, searchDirs, pathTemplate, "")
}

// NewWithPrefix creates a Resolver that strips stripPrefix from module names
// before searching.
func NewWithPrefix(root string, searchDirs []string, pathTemplate, stripPrefix string) *Resolver {
	templates := splitTemplates(pathTemplate)

	return &Resolver{
		Root:        root,
		SearchDirs:  searchDirs,
		Templates:   templates,
		StripPrefix: stripPrefix,
	}
}

func splitTemplates(path string) []string {
	if path == "" {
		return []string{"?.lua", "?/init.lua"}
	}

	parts := strings.Split(path, ";")

	var result []string

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}

	return result
}

// Resolve locates the Lua source file for name, searching fromDir, the
// resolver's root, and its configured search directories, in that order.
func (r *Resolver) Resolve(name string, fromDir string) (*Result, error) {
	primaryLocations := []string{fromDir, r.Root}

	searchLocations := make([]string, 0, len(primaryLocations)+len(r.SearchDirs))
	searchLocations = append(searchLocations, primaryLocations...)
	searchLocations = append(searchLocations, r.SearchDirs...)

	var tried []string

	for _, loc := range searchLocations {
		if loc == "" {
			continue
		}

		// Generate normalized names specific to this location.
		normalizedNames := generateNormalizedNamesForLocation(name, r.StripPrefix, r.Root, loc)
		for _, normalized := range normalizedNames {
			for _, tmpl := range r.Templates {
				candidate := strings.ReplaceAll(tmpl, "?", normalized)
				fullPath := filepath.Join(loc, candidate)

				absPath, err := filepath.Abs(fullPath)
				if err != nil {
					continue
				}

				if fileExists(absPath) {
					canonicalName := canonicalModuleName(name, absPath)

					return &Result{
						FilePath: absPath,
						ModName:  canonicalName,
					}, nil
				}

				tried = append(tried, fullPath)
			}
		}
	}

	return nil, fmt.Errorf("module %q not found in any search location; tried:\n  %s: %w",
		name, strings.Join(tried, "\n  "), ErrModuleNotFound)
}

func generateNormalizedNamesForLocation(name, stripPrefix, rootDir, location string) []string {
	if strings.Contains(name, "/") || strings.HasPrefix(name, ".") {
		return []string{name}
	}

	var namesToTry []string

	namesToTry = append(namesToTry, name)

	// First, apply package prefix stripping.
	if stripPrefix != "" && strings.HasPrefix(name, stripPrefix+".") {
		stripped := strings.TrimPrefix(name, stripPrefix+".")
		namesToTry = append(namesToTry, stripped)
	} else if rootDir != "" {
		// Try to auto-detect prefix from root directory name.
		base := filepath.Base(rootDir)
		normalizedBase := strings.ReplaceAll(base, "-", "_")

		if stripped, ok := strings.CutPrefix(name, normalizedBase+"."); ok {
			namesToTry = append(namesToTry, stripped)
		}
	}

	// For search directories, try stripping the directory name itself
	// e.g., searching in "lib/" for "lib.tuple_config" -> try "tuple_config".
	if location != "" && location != rootDir {
		base := filepath.Base(location)
		if base != "" && base != "." && base != "/" {
			// Check if the current name (or stripped version) starts with base.
			for _, n := range namesToTry {
				if stripped, ok := strings.CutPrefix(n, base+"."); ok {
					namesToTry = append(namesToTry, stripped)
				}
			}
		}
	}

	// Convert each name to path representation.
	var result []string

	seen := make(map[string]bool)

	for _, n := range namesToTry {
		normalized := strings.ReplaceAll(n, ".", string(filepath.Separator))
		if !seen[normalized] {
			seen[normalized] = true
			result = append(result, normalized)
		}
	}

	return result
}

// NormalizeRequireName converts a dotted require() module name into its
// path representation, leaving already-path-like names untouched.
func NormalizeRequireName(name string) string {
	if strings.Contains(name, "/") || strings.HasPrefix(name, ".") {
		return name
	}

	return strings.ReplaceAll(name, ".", string(filepath.Separator))
}

func canonicalModuleName(requireName, _ string) string {
	if strings.Contains(requireName, "/") || strings.HasPrefix(requireName, ".") {
		return requireName
	}

	return requireName
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
