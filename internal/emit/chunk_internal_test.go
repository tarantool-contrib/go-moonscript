package emit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestModuleChunkName: a module's chunk is named as require names the file it
// finds through package.path when the program runs from the root: "./" and
// the path from the root, or the path from the root alone for a file outside
// it.
func TestModuleChunkName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "at the root", path: "/proj/m.lua", want: "@./m.lua"},
		{name: "in a directory", path: "/proj/lib/utils.lua", want: "@./lib/utils.lua"},
		{name: "init module", path: "/proj/pkg/init.lua", want: "@./pkg/init.lua"},
		{name: "in a directory whose name starts with dots", path: "/proj/..lib/m.lua", want: "@./..lib/m.lua"},
		{name: "outside the root", path: "/vendor/json.lua", want: "@../vendor/json.lua"},
		{name: "next to the root", path: "/projlib/m.lua", want: "@../projlib/m.lua"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, moduleChunkName("/proj", tt.path))
		})
	}
}

// TestEntryChunkName: the entry's chunk is named as the command line names
// the script when the program runs from the root: the path from the root.
func TestEntryChunkName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "at the root", path: "/proj/main.lua", want: "@main.lua"},
		{name: "in a directory", path: "/proj/src/main.lua", want: "@src/main.lua"},
		{name: "outside the root", path: "/app/main.lua", want: "@../app/main.lua"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, entryChunkName("/proj", tt.path))
		})
	}
}
