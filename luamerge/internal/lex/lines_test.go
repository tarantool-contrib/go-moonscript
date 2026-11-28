package lex_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/lex"
	"github.com/tarantool/go-luamerge/internal/luatest"
)

// lineProbe is the statement that prints its own line.
const lineProbe = `print(debug.getinfo(1, "l").currentline)`

// TestLexer_Tokens_LinesMatchInterpreters checks the lexer's line numbers
// against the interpreters themselves: each script is a construct that spans
// lines followed by a statement that prints its own line, and the lexer must
// put that statement on the line the interpreter reports.
func TestLexer_Tokens_LinesMatchInterpreters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		// since52 marks what Lua 5.1 cannot read: "\z" and a byte order mark.
		since52 bool
	}{
		{name: "mixed line breaks", src: "local a = 1\r\n\n\r\r\n\n\n", since52: false},
		{name: "CR only", src: "local a = 1\r\r\rlocal b = 2\r", since52: false},
		{name: "form feed and vertical tab", src: "local a\f=\v1\n", since52: false},
		{name: "long string", src: "local s = [==[\r\n\n\r\r\n]]\n]==]\n", since52: false},
		{name: "long comment", src: "--[[\r\r\n\n\r]]\n", since52: false},
		{name: "short comment ended by CR", src: "local a = 1 -- one\rlocal b = 2 -- two\r\n", since52: false},
		{name: "escaped line breaks", src: "local s = 'a\\\r\nb\\\n\rc\\\rd\\\ne'\n", since52: false},
		{name: "z escape", src: "local s = 'a\\z\r\n  \n\r \r\f\v  b'\n", since52: true},
		{name: "shebang", src: "#!/usr/bin/env lua\n\n", since52: false},
		{name: "CR LF shebang", src: "#!/usr/bin/env lua\r\n\r\n", since52: false},
		{name: "byte order mark and shebang", src: lex.BOM + "#!lua\n\r\n", since52: true},
	}

	luatest.ForEachInterpreter(t, func(t *testing.T, interp luatest.Interpreter) {
		t.Helper()

		for _, tt := range tests {
			if tt.since52 && interp.Version == "Lua 5.1" {
				continue
			}

			src := tt.src + lineProbe + "\n"
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "probe.lua"), []byte(src), 0o600))

			result := luatest.Run(t, interp, dir, luatest.RunOptions{LuaPath: "", Env: nil, Timeout: 0}, "probe.lua")
			require.Equal(t, 0, result.ExitCode, "%s: %s", tt.name, result)

			reported, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
			require.NoError(t, err, "%s: %s", tt.name, result)

			tokens, err := lex.NewFile([]byte(src)).Tokens()
			require.NoError(t, err, tt.name)
			require.GreaterOrEqual(t, len(tokens), 1, tt.name)

			// The probe ends the script: its last token is ")" of print(...).
			assert.Equal(t, reported, tokens[len(tokens)-1].Line, "%s: line of the probe", tt.name)
		}
	})
}
