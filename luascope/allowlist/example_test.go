package allowlist_test

import (
	"fmt"

	"github.com/tarantool-contrib/go-moonscript/luascope"
	"github.com/tarantool-contrib/go-moonscript/luascope/allowlist"
)

func ExampleChecker() {
	checker, err := allowlist.NewChecker(allowlist.Rules{
		Modules: []string{"log", "json"},
		Globals: []string{"require", "pairs", "box.space.*"},
	})
	if err != nil {
		panic(err)
	}

	violations, err := checker.Check([]byte(`
local fiber = require('fiber')
box.cfg{}
box.space.users:replace({1})
counter = 1
`), "@migration.lua", luascope.Options{})
	if err != nil {
		panic(err)
	}

	for _, v := range violations {
		fmt.Println(v)
	}

	// Output:
	// 2:15: module 'fiber' is not allowed
	// 3:1: global variable 'box.cfg' is not allowed
	// 5:1: assignment to global variable 'counter'
}
