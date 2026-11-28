package emit

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

// testLongStringText reads the long string literal lit as Lua does and
// returns the text it holds: lit must be one long string from its first byte
// to its last, and the text is what lies between its brackets without the
// line break right after the opening one, which Lua discards. (Lua also reads
// each line break in the text as "\n", which moves no line.)
func testLongStringText(t *testing.T, lit string) string {
	t.Helper()

	tokens, err := lex.New([]byte(lit)).Tokens()
	require.NoError(t, err, "%q", lit)
	require.Len(t, tokens, 1, "%q reads as one token", lit)

	tok := tokens[0]
	require.Equal(t, lex.LongString, tok.Kind, "%q", lit)
	require.Equal(t, []int{0, len(lit)}, []int{tok.Start, tok.End}, "%q is the whole literal", lit)

	level := strings.IndexByte(lit[1:], '[') // The "=" signs of the opening bracket.
	text := lit[level+2 : len(lit)-level-2]

	return text[lex.LineBreak([]byte(text), 0):]
}

// TestLuaLongBracket_SourceEdges: the literal holds the source exactly,
// whatever its first and last bytes. A source that ends in "]", or in "]="
// where the level is 1, formed the closing bracket together with the one
// appended to it, so the string ended early and the --debug bundle did not
// parse. A source that starts with "\r" lost its first line break: Lua reads
// the "\n" after the opening bracket and that "\r" as one line break and
// discards both, so every line of the source moved up by one.
func TestLuaLongBracket_SourceEdges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		// level is the number of "=" signs the literal needs.
		level int
	}{
		{name: "empty", source: "", level: 0},
		{name: "plain", source: "return 1\n", level: 0},
		{name: "ending in ] and a line break", source: "return t[1]\n", level: 0},
		{name: "ending in ]", source: "return t[1]", level: 1},
		{name: "ending in ]=, level 1", source: "return [[x]] -- ]=", level: 2},
		{name: "ending in ]==, level 2", source: "return [[x]] .. [=[y]=] -- ]==", level: 3},
		{name: "ending in a leveled long string", source: "return [=[x]=]", level: 2},
		{name: "closing brackets inside", source: "a = ']]' .. ']=]'\n", level: 2},
		{name: "starting with LF", source: "\nx = 1\n", level: 0},
		{name: "starting with LF CR", source: "\n\rx = 1\n", level: 0},
		{name: "starting with CR", source: "\rx = 1\r", level: 0},
		{name: "starting with CR LF", source: "\r\nx = 1\r\n", level: 0},
		{name: "starting with two CRs", source: "\r\rx = 1", level: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lit := luaLongBracket(tt.source)

			assert.Equal(t, tt.source, testLongStringText(t, lit), "the text of %q", lit)
			assert.True(t, strings.HasPrefix(lit, "["+strings.Repeat("=", tt.level)+"["),
				"%q has level %d", lit, tt.level)
		})
	}
}
