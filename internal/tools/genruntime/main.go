// Command genruntime compiles the MoonScript sources of the runtime's
// pure-Lua modules (runtime/moonsrc/**.moon -> runtime/lua/**.lua) and of the
// moon CLI driver (cmd/moon/moon.moon -> cmd/moon/moon.lua) with this port.
// The outputs are embedded with go:embed; run it through `go generate
// ./runtime` after changing a source or the compiler.
//
// The generated Lua carries no header comment so that its line numbers are
// those of the reference compiler's output (they show up in error messages
// raised by library code, e.g. "moonscript/base.lua:58: ...").
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/runtime/gen"
)

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false, "only report outdated outputs (exit status 1 if any)")
	flag.Parse()

	pairs, err := gen.Sources(os.DirFS(*root))
	if err != nil {
		fail(err)
	}
	stale := 0
	for _, p := range pairs {
		src, err := os.ReadFile(filepath.Join(*root, p.Src))
		if err != nil {
			fail(err)
		}
		code, err := gen.Compile(string(src))
		if err != nil {
			fail(fmt.Errorf("%s: %w", p.Src, err))
		}
		out := filepath.Join(*root, p.Out)
		old, err := os.ReadFile(out)
		if err == nil && string(old) == code {
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			fail(err)
		}
		stale++
		if *check {
			fmt.Fprintf(os.Stderr, "%s is out of date (source %s)\n", p.Out, p.Src)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(out, []byte(code), 0o644); err != nil {
			fail(err)
		}
		fmt.Println("wrote", p.Out)
	}
	if *check && stale > 0 {
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genruntime:", strings.TrimSpace(err.Error()))
	os.Exit(1)
}
