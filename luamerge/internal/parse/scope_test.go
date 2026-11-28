package parse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/go-luamerge/internal/parse"
)

// TestParse_LocalNamedRequire: a local named require that is a function of
// its own, a local function, a local whose value is a function, a parameter
// or a loop variable, hides the global require for the rest of its scope, so
// calls through it are no requires. The walker used to go by the name alone
// and took them for the global: --strict failed on a module the program never
// loads, or the bundler read an unrelated file of that name.
func TestParse_LocalNamedRequire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		{
			name:   "local whose value is a function",
			source: "local require = function(x) return x end\nrequire(\"greeting\")\npcall(require, \"y\")\n",
			want:   []parse.RequireInfo{},
		},
		{
			name:   "local function",
			source: "local function require(name) return name end\nrequire(\"x\")\n",
			want:   []parse.RequireInfo{},
		},
		{
			name:   "local function calls itself",
			source: "local function require(name) return require(name) end\nrequire(\"x\")\n",
			want:   []parse.RequireInfo{},
		},
		{
			name:   "value of the local read before it",
			source: "local require = function(name) return require(name) end\nrequire(\"x\")\n",
			want:   []parse.RequireInfo{testDynamic(1)},
		},
		{
			name:   "parameter",
			source: "local function load(require)\n  return require(\"x\")\nend\nrequire(\"y\")\n",
			want:   []parse.RequireInfo{testStatic("y", 4)},
		},
		{
			name:   "parameter of a function expression",
			source: "local f = function(a, require, ...) return require(\"x\") end\nrequire(\"y\")\n",
			want:   []parse.RequireInfo{testStatic("y", 2)},
		},
		{
			name:   "parameter of a method",
			source: "function M:load(require)\n  return require(\"x\")\nend\nrequire(\"y\")\n",
			want:   []parse.RequireInfo{testStatic("y", 4)},
		},
		{
			name:   "generic for variable",
			source: "for _, require in ipairs(loaders) do\n  require(\"x\")\nend\nrequire(\"y\")\n",
			want:   []parse.RequireInfo{testStatic("y", 4)},
		},
		{
			name:   "numeric for variable",
			source: "for require = 1, 2 do require(\"x\") end\nrequire(\"y\")\n",
			want:   []parse.RequireInfo{testStatic("y", 2)},
		},
		{
			name:   "loop expressions are outside the loop",
			source: "for require in require(\"x\") do end\nfor require = require(\"y\"), 2 do end\n",
			want:   []parse.RequireInfo{testStatic("x", 1), testStatic("y", 2)},
		},
		{
			name: "local of a nested function",
			source: "local function f()\n  local require = function(name) return name end\n" +
				"  return require(\"x\")\nend\nreturn require(\"y\")\n",
			want: []parse.RequireInfo{testStatic("y", 5)},
		},
		{
			name:   "end of the scope of a block",
			source: "do\n  local function require() end\n  require(\"x\")\nend\nrequire(\"y\")\n",
			want:   []parse.RequireInfo{testStatic("y", 5)},
		},
		{
			name: "end of the scope of every kind of block",
			source: "if c then local function require() end elseif d then local function require() end " +
				"else local function require() end x = 1 end\n" +
				"while c do local function require() end x = 1 end\n" +
				"for i = 1, 2 do local function require() end x = 1 end\n" +
				"require(\"y\")\n",
			want: []parse.RequireInfo{testStatic("y", 4)},
		},
		{
			name:   "repeat condition inside the scope of the body",
			source: "repeat local function require() end until require(\"x\")\nrequire(\"y\")\n",
			want:   []parse.RequireInfo{testStatic("y", 2)},
		},
		{
			name: "calls before the local",
			source: "local function f() return require(\"x\") end\nrequire(\"y\")\n" +
				"local function require() end\nrequire(\"z\")\n",
			want: []parse.RequireInfo{testStatic("x", 1), testStatic("y", 2)},
		},
		{
			name:   "local of a local that hides the global",
			source: "local function require() end\ndo\n  local require = require\n  require(\"x\")\nend\n",
			want:   []parse.RequireInfo{},
		},
		{
			name:   "global require through _G",
			source: "local function require() end\n_G.require(\"x\")\nlocal g = _ENV.require\n",
			want:   []parse.RequireInfo{testStatic("x", 2), testIndirect(`_ENV.require aliased as local "g"`, 3)},
		},
		{
			name:   "local that holds the global",
			source: "local function require() end\ndo\n  local require = _G.require\n  require(\"x\")\nend\n",
			want:   []parse.RequireInfo{testStatic("x", 4)},
		},
		{
			name:   "global stored in a local that hides it",
			source: "local require = function() end\nrequire = _G.require\nrequire(\"x\")\n",
			want:   []parse.RequireInfo{testIndirect(`_G.require assigned to "require"`, 2)},
		},
		{
			name:   "Lua 5.5 global declaration",
			source: "local function require() end\ndo\n  global require\n  require(\"x\")\nend\nrequire(\"y\")\n",
			want:   []parse.RequireInfo{testStatic("x", 4)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testParse(t, tt.source))
		})
	}
}

// TestParse_RequireWrapper: a local named require of any other value is a
// wrapper of the global require, such as the lazy require of many Neovim
// plugins (`local require = require("flash.require")`), which loads the
// module of the name it gets. Its calls are taken for requires, so the
// modules are embedded.
func TestParse_RequireWrapper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []parse.RequireInfo
	}{
		{
			name:   "value of a call",
			source: "local require = require(\"lazy\")\nlocal M = require(\"m\")\n",
			want:   []parse.RequireInfo{testStatic("lazy", 1), testStatic("m", 2)},
		},
		{
			name:   "field of a module",
			source: "local require = require(\"loader\").lazy_require\nrequire(\"m\")\npcall(require, \"n\")\n",
			want: []parse.RequireInfo{
				testStatic("loader", 1), testStatic("m", 2), testProtected(testStatic("n", 3)),
			},
		},
		{
			name:   "field of a local",
			source: "local require = loader.require\nrequire(\"m\")\nlocal r = require\n",
			want:   []parse.RequireInfo{testStatic("m", 2), testIndirect(`require aliased as local "r"`, 3)},
		},
		{
			name:   "another local",
			source: "local require = my_require\nrequire(\"m\")\n",
			want:   []parse.RequireInfo{testStatic("m", 2)},
		},
		{
			name:   "one of several values",
			source: "local ok, require = pcall(f)\nlocal a, b = 1, require(\"m\")\n",
			want:   []parse.RequireInfo{testStatic("m", 2)},
		},
		{
			name:   "no value",
			source: "local require\nrequire(\"m\")\n",
			want:   []parse.RequireInfo{testStatic("m", 2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, testParse(t, tt.source))
		})
	}
}
