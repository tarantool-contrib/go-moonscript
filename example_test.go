package luamerge_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/go-luamerge"
)

// Example bundles a tiny two-file Lua project into a single self-contained
// script. Each module is registered into Lua's package.preload table, and the
// bundle ends by requiring the entry module.
func Example() {
	dir, err := os.MkdirTemp("", "luamerge-example-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	defer func() {
		_ = os.RemoveAll(dir)
	}()

	write := func(name, body string) {
		werr := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
		if werr != nil {
			panic(werr)
		}
	}
	write("greet.lua", "local M = {}\nfunction M.hello() return \"hi\" end\nreturn M\n")
	write("main.lua", "local greet = require(\"greet\")\nprint(greet.hello())\n")

	opts := luamerge.DefaultOptions()
	opts.Entry = filepath.Join(dir, "main.lua")

	var buf bytes.Buffer

	res, err := luamerge.Bundle(opts, &buf)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("warnings:", len(res.Warnings))
	fmt.Println("greet preloaded:", bytes.Contains(buf.Bytes(), []byte(`package.preload["greet"]`)))
	fmt.Println("requires entry:", bytes.Contains(buf.Bytes(), []byte(`return require("main")`)))
	// Output:
	// warnings: 0
	// greet preloaded: true
	// requires entry: true
}
