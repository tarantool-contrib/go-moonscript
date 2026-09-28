package integration_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/luatest"
)

// TestDebugBundle_SourceEndings: a --debug bundle embeds the code of each
// file in a long string, which must end where the code ends, whatever its
// last bytes are. Code that ended in "]" without a line break after it, such
// as `return t[1]`, formed the string's closing bracket together with the
// bracket the bundler put after it, and the bundle did not parse; so did
// code ending in "]=" or "]==" once the string needed that level, and code
// that --remove-comments or --minify left ending in "]".
func TestDebugBundle_SourceEndings(t *testing.T) {
	t.Parallel()

	main := "print((require(\"m\")))\n"
	tests := []struct {
		name    string
		project luatest.Project
		flags   []string
	}{
		{
			name:    "entry ending in ]",
			project: luatest.Project{"main.lua": "local t = {42}; print(t[1]); return t[1]"},
			flags:   nil,
		},
		{
			name:    "module ending in ]",
			project: luatest.Project{"main.lua": main, "m.lua": "local t = {7}\nreturn t[1]"},
			flags:   nil,
		},
		{
			name:    "module ending in ]= with ]] inside",
			project: luatest.Project{"main.lua": main, "m.lua": "return [[x]] -- ]="},
			flags:   nil,
		},
		{
			name:    "module ending in ]== with ]] and ]=] inside",
			project: luatest.Project{"main.lua": main, "m.lua": "return [[x]] .. [=[y]=] -- ]=="},
			flags:   nil,
		},
		{
			name:    "module ending in a leveled long string",
			project: luatest.Project{"main.lua": main, "m.lua": "return [=[x]=]"},
			flags:   nil,
		},
		{
			name:    "module ending in ] once its comment is removed",
			project: luatest.Project{"main.lua": main, "m.lua": "local t = {5}\nreturn t[1]--c"},
			flags:   []string{"--remove-comments"},
		},
		{
			name:    "module ending in ] once minified",
			project: luatest.Project{"main.lua": main, "m.lua": "local t = {5}\nreturn t[1] -- the end"},
			flags:   []string{"--minify"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := cliCase(tt.project, append([]string{"--debug"}, tt.flags...)...)
			spec.Check = func(t *testing.T, orig, bundle luatest.Result) {
				t.Helper()

				require.Equal(t, 0, orig.ExitCode, "the original runs\n%s", orig)
				luatest.AssertSame(t, orig, bundle)
			}

			luatest.Differential(t, spec)
		})
	}
}
