package graph_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/config"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/graph"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/resolve"
)

// TestBuild_FileHeader: Build reads every file, the entry, a module and an
// --include package, the way the interpreter loads a file: a byte order mark
// is dropped, and a first line starting with "#" is left empty with its line
// break kept. Nothing else changes, blank lines before the first token
// included, so every line keeps its number, in the source and in what the
// parser reports.
func TestBuild_FileHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// header starts every file.
		header string
		// kept is what is left of header.
		kept string
		// line is the line the first statement is on.
		line int
	}{
		{name: "none", header: "", kept: "", line: 1},
		{name: "blank lines", header: "\n \n\t", kept: "\n \n\t", line: 3},
		{name: "shebang", header: "#!/usr/bin/env lua\n", kept: "\n", line: 2},
		{name: "shebang with a CRLF line break", header: "#!/usr/bin/env lua\r\n", kept: "\n", line: 2},
		{name: "first line starting with # but not #!", header: "# a comment\n\n", kept: "\n\n", line: 3},
		{name: "byte order mark", header: lex.BOM, kept: "", line: 1},
		{name: "byte order mark and shebang", header: lex.BOM + "#!/usr/bin/lua\n", kept: "\n", line: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			dir := t.TempDir()
			for name, body := range map[string]string{
				"main.lua": "require(\"m\")\n",
				"m.lua":    "return 1\n",
				"inc.lua":  "return 2\n",
			} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(tt.header+body), 0o600))
			}

			cfg := config.Default()
			cfg.Entry = filepath.Join(dir, "main.lua")
			cfg.Root = dir
			cfg.Strict = true
			cfg.IncludePackages = []string{"inc"}

			// Act.
			g, err := graph.Build(&cfg, parse.New(), resolve.New(cfg.Root, nil, cfg.Path))

			// Assert.
			require.NoError(t, err)

			sources := make(map[string]string)
			for _, mod := range g.Modules {
				sources[filepath.Base(mod.FilePath)] = string(mod.Source)
			}

			assert.Equal(t, map[string]string{
				"main.lua": tt.kept + "require(\"m\")\n",
				"m.lua":    tt.kept + "return 1\n",
				"inc.lua":  tt.kept + "return 2\n",
			}, sources)

			require.Len(t, g.Entry.Requires, 1)
			assert.Equal(t, tt.line, g.Entry.Requires[0].Line, "the line of the require")
		})
	}
}
