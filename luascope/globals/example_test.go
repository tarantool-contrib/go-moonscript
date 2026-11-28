package globals_test

import (
	"fmt"

	"github.com/tarantool-contrib/go-moonscript/luascope"
	"github.com/tarantool-contrib/go-moonscript/luascope/globals"
)

func ExampleAnalyze() {
	usage, err := globals.Analyze([]byte(`
local log = require('log')
local name = 'users'
box.space[name]:replace({1})
counter = (counter or 0) + 1
`), "=example", luascope.Options{})
	if err != nil {
		panic(err)
	}

	for _, r := range usage.Requires {
		fmt.Println(r.Pos, "require", r.Module)
	}

	for _, a := range usage.Accesses {
		fmt.Println(a.Pos, a.Path, "write:", a.Write)
	}

	// Output:
	// 2:13 require log
	// 2:13 require write: false
	// 4:1 box.space[?].replace write: false
	// 5:1 counter write: true
	// 5:12 counter write: false
}

// The spans of the accesses are the names in the source.
func ExampleAnalyze_spans() {
	src := []byte("local fiber = require('fiber')\nbox.cfg{}\n")

	usage, err := globals.Analyze(src, "=example", luascope.Options{})
	if err != nil {
		panic(err)
	}

	for _, a := range usage.Accesses {
		fmt.Printf("%s %q %s\n", a.Pos, src[a.Span.Start:a.Span.End], a.Path)
	}

	// Output:
	// 1:15 "require" require
	// 2:1 "box" box.cfg
}
