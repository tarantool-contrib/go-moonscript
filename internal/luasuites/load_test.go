//go:build linux

package luasuites

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	lua "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
)

// archiveSource is a Lua file of the archives (isLuaFile), as the parse
// check compiles it (chunkSource).
type archiveSource struct {
	name string // <archive>/<path>
	src  string
}

func archiveSources(tb testing.TB) []archiveSource {
	tb.Helper()
	var srcs []archiveSource
	for _, a := range archives {
		files, err := readArchive(filepath.Join("testdata", "archives", a+".tar.gz"))
		if err != nil {
			tb.Fatal(err)
		}
		for _, f := range files {
			if isLuaFile(f.name) {
				srcs = append(srcs, archiveSource{a + "/" + f.name, chunkSource(f.src)})
			}
		}
	}
	return srcs
}

var dialects = []struct {
	name    string
	dialect lua.Dialect
	parse   parse.Dialect
}{{"lua51", lua.DialectLua51, parse.Lua51}, {"luajit", lua.DialectLuaJIT, parse.LuaJIT}}

// TestASTRoundTrip loads every Lua file of the archives with LoadChunk and
// its parsed tree with LoadAST, in each dialect: the functions dump the
// same (bytecode, constants, lines, locals, upvalues, chunk name), and a
// chunk that parses but does not compile fails both ways (but for the
// register limit of lua5.1, which only source loading checks).
func TestASTRoundTrip(t *testing.T) {
	srcs := archiveSources(t)
	for _, d := range dialects {
		L := lua.NewState(lua.Options{Dialect: d.dialect, SkipOpenLibs: true})
		var same, failed, unparsed int
		for _, s := range srcs {
			chunkname := "@" + s.name
			fnSrc, errSrc := L.LoadChunk(strings.NewReader(s.src), chunkname)
			chunk, err := parse.ParseBytes([]byte(s.src), s.name, d.parse)
			if err != nil {
				if errSrc == nil {
					t.Errorf("%s (%s): loads, but does not parse: %v", s.name, d.name, err)
				}
				unparsed++
				continue
			}
			fnAST, errAST := L.LoadAST(chunk, chunkname)
			switch {
			case errSrc != nil && errAST != nil:
				failed++
				continue
			case errSrc != nil:
				// a check of source loading only: the model of lua5.1's
				// registers
				var ae *lua.ApiError
				var pe *parse.Error
				if !errors.As(errSrc, &ae) || !errors.As(ae.Cause, &pe) || d.dialect != lua.DialectLua51 ||
					pe.Message != "function or expression too complex" {
					t.Errorf("%s (%s): LoadChunk fails (%v), LoadAST does not", s.name, d.name, errSrc)
				}
				continue
			case errAST != nil:
				t.Errorf("%s (%s): LoadAST fails: %v", s.name, d.name, errAST)
				continue
			}
			dumpSrc, err1 := lua.DumpFunction(fnSrc)
			dumpAST, err2 := lua.DumpFunction(fnAST)
			if err1 != nil || err2 != nil {
				t.Fatal(err1, err2)
			}
			if !bytes.Equal(dumpSrc, dumpAST) {
				t.Errorf("%s (%s): LoadAST's dump differs from LoadChunk's", s.name, d.name)
				continue
			}
			same++
		}
		L.Close()
		t.Logf("%s: %d files: %d dump the same, %d fail to compile both ways, %d don't parse", d.name, len(srcs), same, failed, unparsed)
	}
}

// BenchmarkLoadArchives parses and compiles every Lua file of the archives,
// in each dialect: with LState.LoadChunk (source), with parse.ParseBytes
// and LState.LoadAST (ast), and with LoadAST alone on the parsed trees
// (ast-only: the checks and the compiler).
func BenchmarkLoadArchives(b *testing.B) {
	srcs := archiveSources(b)
	for _, d := range dialects {
		b.Run(d.name+"/source", func(b *testing.B) {
			L := lua.NewState(lua.Options{Dialect: d.dialect, SkipOpenLibs: true})
			defer L.Close()
			for b.Loop() {
				for _, s := range srcs {
					_, _ = L.LoadChunk(strings.NewReader(s.src), "=src")
				}
			}
		})
		b.Run(d.name+"/ast", func(b *testing.B) {
			L := lua.NewState(lua.Options{Dialect: d.dialect, SkipOpenLibs: true})
			defer L.Close()
			for b.Loop() {
				for _, s := range srcs {
					if chunk, err := parse.ParseBytes([]byte(s.src), "src", d.parse); err == nil {
						_, _ = L.LoadAST(chunk, "=src")
					}
				}
			}
		})
		b.Run(d.name+"/ast-only", func(b *testing.B) {
			L := lua.NewState(lua.Options{Dialect: d.dialect, SkipOpenLibs: true})
			defer L.Close()
			var chunks [][]ast.Stmt
			for _, s := range srcs {
				if chunk, err := parse.ParseBytes([]byte(s.src), "src", d.parse); err == nil {
					chunks = append(chunks, chunk)
				}
			}
			for b.Loop() {
				for _, chunk := range chunks {
					_, _ = L.LoadAST(chunk, "=src")
				}
			}
		})
	}
}
