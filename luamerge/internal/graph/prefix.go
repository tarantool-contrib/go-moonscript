package graph

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/go-luamerge/internal/parse"
)

// applyPackagePrefix makes the bundle a library that shows the program which
// loads it names with the package prefix only. Every module is registered
// under the prefixed form of each of its names (see prefixed) and under no
// other. The project's code keeps finding its modules as every require of a
// module the bundle embeds gets the prefix put into its module name, in the
// code the bundle embeds (see renameRequires); a require the bundle leaves to
// the runtime keeps its name.
//
// A name written as a path is a name like any other: "./x" becomes "p../x",
// a module of its own, as "./x" is natively, which package.searchpath finds
// under the prefix's directory as it finds "./x" under the root. A name some
// runtime has a built-in module of becomes a Deferral when the prefix changes
// it.
//
// It fails with an error wrapping ErrNameConflict when a name the prefix
// generates is taken: by a module of the project that has that name, or by a
// module some runtime has built in. The bundle registers one module under a
// name, so the one would take the place of the other.
func applyPackagePrefix(g *Graph, packagePrefix string) error {
	if packagePrefix == "" {
		return nil
	}

	// The modules by the names they have before the prefix: a require of one
	// of these names loads a module the bundle embeds.
	owners := make(map[string]*Module)

	for _, mod := range g.Modules {
		for _, name := range mod.Names {
			owners[name] = mod
		}
	}

	for _, mod := range g.Modules {
		err := prefixNames(mod, packagePrefix, owners)
		if err != nil {
			return err
		}
	}

	for _, mod := range g.Modules {
		err := renameRequires(mod, packagePrefix, owners)
		if err != nil {
			return err
		}
	}

	return nil
}

// prefixed returns the prefixed form of name: the package prefix, a dot and
// name, or name itself when it carries the prefix already.
func prefixed(packagePrefix, name string) string {
	if strings.HasPrefix(name, packagePrefix+".") {
		return name
	}

	return packagePrefix + "." + name
}

// prefixNames replaces the names of mod with their prefixed forms, and those
// of its BuiltinNames that the prefix changes with Deferrals. owners are the
// modules by the names they have before the prefix. It fails when a name the
// prefix generates is taken (see checkGenerated).
func prefixNames(mod *Module, packagePrefix string, owners map[string]*Module) error {
	var (
		names, builtinNames []string
		deferrals           []Deferral
	)

	for _, name := range mod.Names {
		registered := prefixed(packagePrefix, name)
		if registered != name {
			owner := owners[registered]
			if owner == mod {
				// Another name of the module is the generated one already.
				owner = nil
			}

			err := checkGenerated(packagePrefix, name, registered, mod, owner)
			if err != nil {
				return err
			}
		}

		if !slices.Contains(names, registered) {
			names = append(names, registered)
		}

		switch {
		case !slices.Contains(mod.BuiltinNames, name):
		case registered == name:
			builtinNames = append(builtinNames, name)
		default:
			deferrals = append(deferrals, Deferral{Name: registered, Builtin: name})
		}
	}

	mod.Names, mod.BuiltinNames, mod.Deferrals = names, builtinNames, deferrals

	return nil
}

// checkGenerated returns an error wrapping ErrNameConflict when prefixed, the
// name the package prefix generates from name, a name of mod, is taken: by
// owner, the module of the project that has that name, when not nil, or by a
// module some runtime has built in.
func checkGenerated(packagePrefix, name, prefixed string, mod, owner *Module) error {
	turns := fmt.Sprintf("package prefix %q turns %q (%s) into %q", packagePrefix, name, mod.FilePath, prefixed)

	if owner != nil {
		return fmt.Errorf("%w: %s, the name of %s; choose another prefix", ErrNameConflict, turns, owner.FilePath)
	}

	providers := builtinRuntimes(prefixed)
	if providers == 0 {
		return nil
	}

	verb := "have"
	if len(providers.names()) == 1 {
		verb = "has"
	}

	return fmt.Errorf("%w: %s, the name of a module %s %s built in; choose another prefix",
		ErrNameConflict, turns, providers, verb)
}

// renameRequires puts the package prefix into the module name of every
// require in the code of mod that loads a module the bundle embeds, one of
// owners by a name it has before the prefix, and lacks the prefix: the
// bundle registers the prefixed names only. The prefix goes into the string
// literal of the name, in place and on its lines (see parse.PrefixNames), so
// the transforms read the renamed code and every line keeps its number.
//
// A require the bundle leaves to the runtime keeps its name: of a module of
// the standard library, of a runtime's built-in module the project has no
// file for, of a module skipped or not found, a C module, a dynamic require.
// So does a call of a local named require, which the parser does not take
// for a require; in a file only the token scanner reads, which knows no
// scopes, it does.
func renameRequires(mod *Module, packagePrefix string, owners map[string]*Module) error {
	var calls []parse.RequireInfo

	for _, call := range mod.Requires {
		if call.Static && owners[call.Name] != nil && prefixed(packagePrefix, call.Name) != call.Name {
			calls = append(calls, call)
		}
	}

	if len(calls) == 0 {
		return nil
	}

	source, err := parse.PrefixNames(mod.Source, calls, packagePrefix+".")
	if err != nil {
		return fmt.Errorf("prefix the requires of %s: %w", mod.FilePath, err)
	}

	mod.Source = source

	return nil
}
