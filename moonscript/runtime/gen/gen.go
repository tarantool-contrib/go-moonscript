// Package gen lists the MoonScript sources that are precompiled into the
// runtime (see internal/tools/genruntime) and compiles them.
package gen

import (
	"io/fs"
	"path"
	"strings"

	moonscript "github.com/tarantool-contrib/go-moonscript/moonscript"
)

// Pair is a MoonScript source and its generated Lua file, as slash paths
// relative to the repository root.
type Pair struct{ Src, Out string }

// Sources lists moonscript/runtime/moonsrc/**/*.moon (compiled to
// moonscript/runtime/lua/) and
// cmd/moon/moon.moon (compiled next to itself) in root.
func Sources(root fs.FS) ([]Pair, error) {
	var pairs []Pair
	err := fs.WalkDir(root, "moonscript/runtime/moonsrc", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".moon" {
			return err
		}
		rel := strings.TrimPrefix(p, "moonscript/runtime/moonsrc/")
		pairs = append(pairs, Pair{p, "moonscript/runtime/lua/" + strings.TrimSuffix(rel, ".moon") + ".lua"})
		return nil
	})
	if err != nil {
		return nil, err
	}
	pairs = append(pairs, Pair{"cmd/moon/moon.moon", "cmd/moon/moon.lua"})
	return pairs, nil
}

// Compile is what moonc writes for src: to_lua with default options plus a
// trailing newline.
func Compile(src string) (string, error) {
	code, _, err := moonscript.ToLua(src, moonscript.Options{})
	if err != nil {
		return "", err
	}
	return code + "\n", nil
}
