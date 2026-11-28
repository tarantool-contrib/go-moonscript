package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/internal/grammar"
)

// TestUpToDate checks that moonscript/parse/parser_gen.go is what the generator
// produces from the current grammar (run go generate ./parse otherwise).
func TestUpToDate(t *testing.T) {
	want, err := Generate(grammar.Moon())
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../../moonscript/parse/parser_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("moonscript/parse/parser_gen.go is stale: run go generate ./moonscript/parse")
	}
}
