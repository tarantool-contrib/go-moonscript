package emit

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/luatest"
)

// TestQuoteLua_PadsDecimalEscapes: a byte written as a decimal escape takes
// all three digits, so a digit after it cannot join the escape. Byte 1
// followed by "2" was written `\12`, which Lua reads as byte 12, a form feed.
func TestQuoteLua_PadsDecimalEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "control byte before a digit", input: "\x012", want: `"\0012"`},
		{name: "NUL before a digit", input: "\x000", want: `"\0000"`},
		{name: "unit separator before a digit", input: "\x1f9", want: `"\0319"`},
		{name: "DEL before a digit", input: "\x7f1", want: `"\1271"`},
		{name: "control byte between letters", input: "a\x01b", want: `"a\001b"`},
		{name: "quote and backslash", input: `a"b\c`, want: `"a\"b\\c"`},
		{name: "line breaks", input: "a\nb\rc", want: `"a\nb\rc"`},
		{name: "bytes above 127 as they are", input: "é", want: `"é"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, quoteLua(tt.input))
		})
	}
}

// quoteInputs are the strings the round trip quotes: every byte alone, every
// byte quoteLua escapes followed by every digit, and a few mixed ones.
func quoteInputs() []string {
	inputs := []string{"", "\x012", "a\"b\\c", "a\nb\rc\td", "\x00\x00\x00", "é", "require.me"}

	for value := range 256 {
		inputs = append(inputs, string([]byte{byte(value)}))
	}

	for value := range 256 {
		if value >= 0x20 && value != 0x7f {
			continue
		}

		for digit := '0'; digit <= '9'; digit++ {
			inputs = append(inputs, string([]byte{byte(value), byte(digit)}))
		}
	}

	return inputs
}

// testByteList is the bytes of s as decimal numbers separated by spaces.
func testByteList(s string) string {
	numbers := make([]string, 0, len(s))
	for i := range len(s) {
		numbers = append(numbers, strconv.Itoa(int(s[i])))
	}

	return strings.Join(numbers, " ")
}

// TestQuoteLua_RoundTrips: every interpreter loads each literal quoteLua
// writes as the string it was given, byte for byte. The script loads each
// literal as a chunk of its own, so one that does not load is reported on
// its own line.
func TestQuoteLua_RoundTrips(t *testing.T) {
	t.Parallel()

	var script, want strings.Builder

	script.WriteString("local load = loadstring or load\nlocal literals = {\n")

	for _, input := range quoteInputs() {
		script.WriteString(luaLongBracket(quoteLua(input)) + ",\n")
		want.WriteString(testByteList(input) + "\n")
	}

	script.WriteString("}\n" +
		"for _, literal in ipairs(literals) do\n" +
		"  local chunk, err = load(\"return \" .. literal)\n" +
		"  if chunk then\n" +
		"    local s, bytes = chunk(), {}\n" +
		"    for i = 1, #s do bytes[i] = s:byte(i) end\n" +
		"    print(table.concat(bytes, \" \"))\n" +
		"  else\n" +
		"    print(\"does not load: \" .. literal .. \": \" .. err)\n" +
		"  end\n" +
		"end\n")

	opts := luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		dir := luatest.WriteProject(t, luatest.Project{"quote.lua": script.String()})
		r := luatest.Run(t, interp, dir, opts, "quote.lua")
		require.Equal(t, 0, r.ExitCode, "%s", r)
		assert.Equal(t, want.String(), r.Stdout, "the bytes each literal holds, one literal per line")
	})
}
