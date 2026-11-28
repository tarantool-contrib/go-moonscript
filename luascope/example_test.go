package luascope_test

import (
	"errors"
	"fmt"

	"github.com/tarantool-contrib/go-moonscript/lua/parse"
	"github.com/tarantool-contrib/go-moonscript/luascope"
	"github.com/tarantool-contrib/go-moonscript/luascope/allowlist"
	"github.com/tarantool-contrib/go-moonscript/luascope/globals"
)

func Example() {
	usage, err := globals.Analyze([]byte("box.space.users:select()"), "@example.lua",
		luascope.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, access := range usage.Accesses {
		fmt.Println(access.Pos, access.Path, access.Write)
	}

	// Output:
	// 1:1 box.space.users.select false
}

// A lint pipeline parses and resolves a chunk once and passes the tree and
// its resolution to every analysis.
func ExampleParse() {
	src := []byte("local log = require('log')\nbox.cfg{}\n")

	chunk, info, err := luascope.Parse(src, "@migration.lua", luascope.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, r := range globals.AnalyzeChunk(chunk, info).Requires {
		fmt.Println(r.Pos, "require", r.Module)
	}

	checker, err := allowlist.NewChecker(allowlist.Rules{Modules: []string{"log"}})
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, v := range checker.CheckChunk(chunk, info) {
		fmt.Println(v)
	}

	_, _, err = luascope.Parse([]byte("x = 1 +"), "@migration.lua", luascope.Options{})

	var syntaxErr *parse.Error
	if errors.As(err, &syntaxErr) {
		fmt.Println(syntaxErr.Pos.Line, syntaxErr.Message, syntaxErr.Token)
		fmt.Println(err)
	}

	// Output:
	// 1:13 require log
	// 1:13: global variable 'require' is not allowed
	// 2:1: global variable 'box.cfg' is not allowed
	// 1 unexpected symbol <eof>
	// migration.lua:1: unexpected symbol near '<eof>'
}
