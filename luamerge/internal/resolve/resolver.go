// Package resolve implements Lua module name resolution against a
// configurable search path, mirroring Lua's package.searchpath.
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

// Resolver locates Lua source files for require()-style module names the way
// Lua's package.searchpath does, with every template relative to the root
// and then relative to each search directory; see Resolve.
type Resolver struct {
	Root        string   // base directory: where the program runs.
	SearchDirs  []string // additional directories to search, after the root.
	Templates   []string // e.g. ["?.lua", "?/init.lua"].
	StripPrefix string   // if set, strip this prefix from module names (e.g., "tuple_diff").
}

// Result is the outcome of a successful module resolution.
type Result struct {
	FilePath string // absolute normalized path to .lua file.
	ModName  string // the module name as required.
}

// New creates a Resolver with no package-prefix stripping.
func New(root string, searchDirs []string, pathTemplate string) *Resolver {
	return NewWithPrefix(root, searchDirs, pathTemplate, "")
}

// NewWithPrefix creates a Resolver that strips stripPrefix from module names
// the native lookup cannot find.
func NewWithPrefix(root string, searchDirs []string, pathTemplate, stripPrefix string) *Resolver {
	templates := splitTemplates(pathTemplate)

	return &Resolver{
		Root:        root,
		SearchDirs:  searchDirs,
		Templates:   templates,
		StripPrefix: stripPrefix,
	}
}

// defaultTemplates are the templates Resolve falls back to: what --path uses
// when it is empty, and what an empty entry within --path expands to, in
// place (see splitTemplates).
func defaultTemplates() []string {
	return []string{"?.lua", "?/init.lua"}
}

func splitTemplates(path string) []string {
	if path == "" {
		return defaultTemplates()
	}

	parts := strings.Split(path, ";")

	var result []string

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			// A leading, trailing or doubled ";" produces an empty entry,
			// the same way Lua's own LUA_PATH reads a doubled ";;" as "the
			// default path here"; expand it instead of dropping it, or a
			// caller who appends a search location to --path loses the
			// built-in templates they meant to keep alongside it.
			result = append(result, defaultTemplates()...)
			continue
		}

		result = append(result, part)
	}

	return result
}

// Resolve locates the Lua source file for the module name like Lua's
// package.searchpath: the name with every "." replaced by the directory
// separator (see NormalizeRequireName) is substituted for every "?" of each
// template, and the first file that can be read wins, trying all templates
// relative to the root and then relative to each search directory, in order.
// Where the name is required from plays no part: Lua resolves every name
// against package.path, relative to the working directory, so a name means
// the same file wherever it is required.
//
// Only when that lookup finds nothing anywhere are the name extensions tried
// (see extensionNames). Whenever Lua itself would load a file, Resolve returns
// that same file.
func (r *Resolver) Resolve(name string) (*Result, error) {
	var tried []string

	for _, candidate := range r.candidates(name) {
		file, err := filepath.Abs(candidate)
		if err != nil {
			return nil, fmt.Errorf("resolve module %q: %w", name, err)
		}

		if readable(file) {
			return &Result{FilePath: file, ModName: name}, nil
		}

		tried = append(tried, candidate)
	}

	return nil, fmt.Errorf("module %q not found in any search location; tried:\n  %s: %w",
		name, strings.Join(tried, "\n  "), ErrModuleNotFound)
}

// candidates are the files Resolve tries for name, in order: first the native
// lookup at every location, then the extension names at every location. A
// file that comes up twice is tried once.
func (r *Resolver) candidates(name string) []string {
	locations := r.locations()

	var files []string

	for _, loc := range locations {
		files = append(files, r.files(loc, name)...)
	}

	for _, loc := range locations {
		for _, alt := range r.extensionNames(name, loc) {
			files = append(files, r.files(loc, alt)...)
		}
	}

	seen := make(map[string]bool, len(files))
	unique := files[:0]

	for _, file := range files {
		if !seen[file] {
			seen[file] = true
			unique = append(unique, file)
		}
	}

	return unique
}

// locations are the directories the templates are relative to: the root,
// then each search directory.
func (r *Resolver) locations() []string {
	locations := make([]string, 0, 1+len(r.SearchDirs))

	for _, loc := range append([]string{r.Root}, r.SearchDirs...) {
		if loc != "" {
			locations = append(locations, loc)
		}
	}

	return locations
}

// files are the candidate files for name at loc, one per template. A
// relative template is joined under loc, as package.searchpath joins a
// relative package.path entry under the current directory; an absolute
// template names the same file at every location, so it is used as is,
// without loc, and candidates' final dedup collapses the repeats that
// produces into the single try it deserves.
func (r *Resolver) files(loc, name string) []string {
	path := NormalizeRequireName(name)
	files := make([]string, 0, len(r.Templates))

	for _, tmpl := range r.Templates {
		substituted := strings.ReplaceAll(tmpl, "?", path)

		if filepath.IsAbs(tmpl) {
			files = append(files, filepath.Clean(substituted))
			continue
		}

		files = append(files, filepath.Join(loc, substituted))
	}

	return files
}

// extensionNames are the names tried at loc once the native lookup of name
// has failed at every location. They are the documented extensions over
// package.searchpath, in order: name without the explicit strip prefix or,
// when that does not apply and the root directory's name contains a hyphen,
// without that name (the hyphen read as an underscore) — a root name without
// a hyphen is never auto-stripped, only --strip-prefix or --package-name
// does that for it; then, at a search directory, name and the stripped name
// without the directory's own name.
func (r *Resolver) extensionNames(name, loc string) []string {
	var names []string

	if r.StripPrefix != "" && strings.HasPrefix(name, r.StripPrefix+".") {
		names = append(names, strings.TrimPrefix(name, r.StripPrefix+"."))
	} else if r.Root != "" {
		rootBase := filepath.Base(r.Root)
		if strings.Contains(rootBase, "-") {
			base := strings.ReplaceAll(rootBase, "-", "_")
			if stripped, ok := strings.CutPrefix(name, base+"."); ok {
				names = append(names, stripped)
			}
		}
	}

	if loc != r.Root {
		// Searching in "lib/" for "lib.tuple_config" also tries "tuple_config".
		base := filepath.Base(loc)
		if base != "." && base != string(filepath.Separator) {
			for _, n := range append([]string{name}, names...) {
				if stripped, ok := strings.CutPrefix(n, base+"."); ok {
					names = append(names, stripped)
				}
			}
		}
	}

	return names
}

// NormalizeRequireName converts a require() module name into its path
// representation the way package.searchpath does: every "." becomes the
// directory separator, in a name written as a path too. So "./foo", "../foo"
// and ".foo" all name foo relative to the location searched, "a/b.c" names
// a/b/c, and no name can climb out of that location with "..".
func NormalizeRequireName(name string) string {
	return strings.ReplaceAll(name, ".", string(filepath.Separator))
}

// readable reports whether path can be opened for reading, which is Lua's own
// test for a candidate file (a directory passes it, as it does fopen).
func readable(path string) bool {
	file, err := os.Open(path) // #nosec G304 -- a candidate built from the configured search path
	if err != nil {
		return false
	}

	_ = file.Close()

	return true
}
