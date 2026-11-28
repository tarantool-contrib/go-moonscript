package lua

// go-moonscript patch: tests of string.dump's chunks (dump.go) beyond the
// compat script lib_string_dump.lua: every function of the Lua files of the
// tree dumps, loads and dumps again to the same bytes, and corrupt chunks
// load as an error or a function, never a panic (they are not run: like
// PUC Lua's checked bytecode, a corrupt one may loop or allocate without
// bounds).

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/lua/parse"
)

func TestDumpRoundTrip(t *testing.T) {
	var files []string
	for _, root := range []string{"testdata", "../moonscript/runtime/lua"} {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".lua") {
				files = append(files, p)
			}
			return nil
		})
	}
	if len(files) < 50 {
		t.Fatalf("only %d files", len(files))
	}
	for _, p := range files {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		chunk, err := parse.Parse(bytes.NewReader(src), p)
		if err != nil {
			continue
		}
		proto, err := Compile(chunk, p)
		if err != nil {
			continue
		}
		b, err := DumpFunction(&LFunction{Proto: proto})
		if err != nil {
			t.Fatal(err)
		}
		fp, why, _ := undumpChunk(b)
		if fp == nil {
			t.Errorf("%s: %s", p, why)
			continue
		}
		if b2, _ := DumpFunction(&LFunction{Proto: fp}); !bytes.Equal(b, b2) {
			t.Errorf("%s: dumped again, the chunk differs", p)
		}
	}
}

func TestDumpCorrupt(t *testing.T) {
	L := NewState()
	defer L.Close()
	if err := L.DoString(`chunk = string.dump(function(a, ...)
  local t = {a, ...}
  for i = 1, #t do t[i] = tostring(t[i]) .. "x" end
  return table.concat(t, ","), function() return t end
end)`); err != nil {
		t.Fatal(err)
	}
	chunk := []byte(string(L.GetGlobal("chunk").(LString)))
	try := func(b []byte) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic loading %q: %v", b, r)
			}
		}()
		if _, err := L.LoadChunk(bytes.NewReader(b), "=chunk"); err != nil &&
			!(strings.HasPrefix(err.Error(), "chunk: ") && strings.HasSuffix(err.Error(), " in precompiled chunk")) {
			t.Fatalf("error %q", err)
		}
	}
	for n := 0; n < len(chunk); n++ {
		try(chunk[:n])
	}
	for i := len(dumpHeader); i < len(chunk); i++ {
		for _, x := range []byte{0x01, 0x80, 0xff} {
			b := bytes.Clone(chunk)
			b[i] ^= x
			try(b)
		}
	}
}
