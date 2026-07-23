package parse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/parse"
)

func TestParseStaticRequire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		expected []parse.RequireInfo
	}{
		{
			name:   "simple require",
			source: `require("foo")`,
			expected: []parse.RequireInfo{
				{Name: "foo", Line: 1, Static: true},
			},
		},
		{
			name:   "require with dots",
			source: `require("foo.bar")`,
			expected: []parse.RequireInfo{
				{Name: "foo.bar", Line: 1, Static: true},
			},
		},
		{
			name: "multiple requires",
			source: `local a = require("a")
local b = require("b")`,
			expected: []parse.RequireInfo{
				{Name: "a", Line: 1, Static: true},
				{Name: "b", Line: 2, Static: true},
			},
		},
		{
			name:   "pcall require",
			source: `pcall(require, "module")`,
			expected: []parse.RequireInfo{
				{Name: "module", Line: 1, Static: true},
			},
		},
		{
			name:   "dynamic require",
			source: `require(var)`,
			expected: []parse.RequireInfo{
				{Name: "", Line: 1, Static: false},
			},
		},
		{
			name: "require in function",
			source: `function f()
  require("inner")
end`,
			expected: []parse.RequireInfo{
				{Name: "inner", Line: 2, Static: true},
			},
		},
		{
			name:     "no requires",
			source:   `print("hello")`,
			expected: []parse.RequireInfo{},
		},
	}

	parser := parse.New()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parser.Parse([]byte(tt.source), "test.lua")
			require.NoError(t, err, "Parse() error")
			require.Len(t, got, len(tt.expected), "Parse() requires count")

			for i := range got {
				assert.Equal(t, tt.expected[i].Name, got[i].Name, "require %d: Name", i)
				assert.Equal(t, tt.expected[i].Line, got[i].Line, "require %d: Line", i)
				assert.Equal(t, tt.expected[i].Static, got[i].Static, "require %d: Static", i)
			}
		})
	}
}
