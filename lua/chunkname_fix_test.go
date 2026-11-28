package lua

// go-moonscript patch: Lua chunk names on a plain state (chunkname.go):
// ChunkID, LState.Load's display names, LoadChunk, LoadFile and DoFile as
// luaL_loadfile (chunk name, messages, "#" line), debug.getinfo's source
// and short_src. The Lua side is in testdata/compat/lib_chunkname.lua,
// lib_loadfile.lua and lib_require.lua.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChunkID(t *testing.T) {
	long := strings.Repeat("x", 50)
	path := strings.Repeat("d/", 30) + "f.lua" // 65 bytes
	cases := []struct {
		in, lua51, luajit string
	}{
		{"@q.lua", "q.lua", "q.lua"},
		{"=stdin", "stdin", "stdin"},
		{"return 1", `[string "return 1"]`, `[string "return 1"]`},
		{"x = 1\ny = 2", `[string "x = 1..."]`, `[string "x = 1..."]`},
		{"x = 1\ty = 2", `[string "x = 1	y = 2"]`, `[string "x = 1..."]`},
		{"", `[string ""]`, `[string ""]`},
		{long, `[string "` + long[:43] + `..."]`, `[string "` + long[:45] + `..."]`},
		{"@" + path, "..." + path[len(path)-52:], "..." + path[len(path)-56:]},
		{"=" + long + long, (long + long)[:59], (long + long)[:59]},
		{"return 1\x00rest", `[string "return 1"]`, `[string "return 1"]`},
		{"@a\x00b", "a", "a"},
	}
	for _, c := range cases {
		if got := ChunkID(c.in, DialectLua51); got != c.lua51 {
			t.Errorf("ChunkID(%q, DialectLua51) = %q, want %q", c.in, got, c.lua51)
		}
		if got := ChunkID(c.in, DialectLuaJIT); got != c.luajit {
			t.Errorf("ChunkID(%q, DialectLuaJIT) = %q, want %q", c.in, got, c.luajit)
		}
	}
	// Lua 5.1's syntax errors show ids of up to 79 bytes (MAXSRC)
	if got, want := syntaxChunkID(long+long, DialectLua51), `[string "`+(long + long)[:63]+`..."]`; got != want {
		t.Errorf("syntaxChunkID = %q, want %q", got, want)
	}
}

// getinfoS returns debug.getinfo(fn, "S")'s source and short_src.
func getinfoS(t *testing.T, L *LState, fn *LFunction) (string, string) {
	t.Helper()
	if err := L.CallByParam(P{Fn: L.GetField(L.GetGlobal("debug"), "getinfo"), NRet: 1, Protect: true}, fn, LString("S")); err != nil {
		t.Fatal(err)
	}
	info := L.Get(-1).(*LTable)
	L.Pop(1)
	return info.RawGetString("source").String(), info.RawGetString("short_src").String()
}

func writeFile(t *testing.T, name, text string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlainLoadFile(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFile(t, "q.lua", "local i = debug.getinfo(1, 'S')\nsrc, short = i.source, i.short_src\n")
	writeFile(t, "err.lua", "local x = 1\nerror('boom')\n")
	writeFile(t, "syn.lua", "local x = 1\nx = = 2\n")
	writeFile(t, "sheb.lua", "#!/usr/bin/lua\n\nerror('sheb')\n")
	if err := os.Mkdir("dir", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []Dialect{DialectLua51, DialectLuaJIT} {
		L := NewState(Options{Dialect: d})
		if err := L.DoFile("q.lua"); err != nil {
			t.Fatal(err)
		}
		if src, short := L.GetGlobal("src").String(), L.GetGlobal("short").String(); src != "@q.lua" || short != "q.lua" {
			t.Errorf("DoFile: source %q, short_src %q", src, short)
		}
		fn, err := L.LoadFile("q.lua")
		if err != nil {
			t.Fatal(err)
		}
		if src, short := getinfoS(t, L, fn); src != "@q.lua" || short != "q.lua" {
			t.Errorf("LoadFile: source %q, short_src %q", src, short)
		}
		dbg := &Debug{}
		if _, err := L.GetInfo(">S", dbg, fn); err != nil || dbg.Source != "q.lua" || dbg.ChunkName != "@q.lua" {
			t.Errorf("GetInfo: Source %q, ChunkName %q, %v", dbg.Source, dbg.ChunkName, err)
		}
		for file, want := range map[string]string{
			"err.lua":  "err.lua:2: boom",
			"sheb.lua": "sheb.lua:3: sheb",
		} {
			err := L.DoFile(file)
			var ae *ApiError
			if !errors.As(err, &ae) || ae.Type != ApiErrorRun || !strings.HasPrefix(ae.Object.String(), want) {
				t.Errorf("DoFile(%q) = %v, want %q", file, err, want)
			}
		}
		for file, want := range map[string]string{
			"syn.lua":     "syn.lua:2: unexpected symbol near '='",
			"missing.lua": "cannot open missing.lua: No such file or directory",
			"dir":         "cannot read dir: Is a directory",
		} {
			_, err := L.LoadFile(file)
			var ae *ApiError
			if !errors.As(err, &ae) || ae.Object.String() != want {
				t.Errorf("LoadFile(%q) = %v, want %q", file, err, want)
			}
			if err2 := L.DoFile(file); err2 == nil || err2.Error() != want {
				t.Errorf("DoFile(%q) = %v, want %q", file, err2, want)
			}
		}
		_, err = L.LoadFile("missing.lua")
		var ae *ApiError
		if !errors.As(err, &ae) || ae.Type != ApiErrorFile || !errors.Is(ae.Cause, fs.ErrNotExist) {
			t.Errorf("LoadFile(missing): %#v", err)
		}
		L.Close()
	}
}

func TestPlainLoadFilePermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	name := filepath.Join(t.TempDir(), "noperm.lua")
	writeFile(t, name, "return 1")
	if err := os.Chmod(name, 0); err != nil {
		t.Fatal(err)
	}
	L := NewState()
	defer L.Close()
	want := "cannot open " + name + ": Permission denied"
	if _, err := L.LoadFile(name); err == nil || err.Error() != want {
		t.Errorf("LoadFile = %v, want %q", err, want)
	}
	if err := L.CallByParam(P{Fn: L.GetGlobal("loadfile"), NRet: 2, Protect: true}, LString(name)); err != nil {
		t.Fatal(err)
	}
	if got := L.Get(-1).String(); got != want {
		t.Errorf("loadfile: %q, want %q", got, want)
	}
}

func TestLoadDisplayName(t *testing.T) {
	L := NewState()
	defer L.Close()
	long := strings.Repeat("n", 70)
	for _, name := range []string{"disp", "@at", long} {
		fn, err := L.Load(strings.NewReader("local x\nerror('e')"), name)
		if err != nil {
			t.Fatal(err)
		}
		if src, short := getinfoS(t, L, fn); src != "="+name || short != name {
			t.Errorf("Load(%q): source %q, short_src %q", name, src, short)
		}
		L.Push(fn)
		if err := L.PCall(0, 0, nil); err == nil || !strings.HasPrefix(err.Error(), name+":2: e") {
			t.Errorf("Load(%q): error %v", name, err)
		}
		if _, err := L.Load(strings.NewReader("x = = 1"), name); err == nil || err.Error() != name+":1: unexpected symbol near '='" {
			t.Errorf("Load(%q): syntax error %v", name, err)
		}
	}
	// a function from Compile, which has no chunk name
	chunk, err := L.Load(strings.NewReader("return 1"), "c")
	if err != nil {
		t.Fatal(err)
	}
	fp := *chunk.Proto
	fp.Source, fp.hasSource = "", false
	if src, _ := getinfoS(t, L, &LFunction{Proto: &fp, Env: L.G.Global}); src != "=c" {
		t.Errorf("no chunk name: source %q", src)
	}
}

func TestLoadChunk(t *testing.T) {
	L := NewState()
	defer L.Close()
	for _, c := range []struct{ chunkname, id string }{
		{"@x.lua", "x.lua"},
		{"=named", "named"},
		{"local x\nerror('e')", `[string "local x..."]`},
	} {
		fn, err := L.LoadChunk(strings.NewReader("local x\nerror('e')"), c.chunkname)
		if err != nil {
			t.Fatal(err)
		}
		if src, short := getinfoS(t, L, fn); src != c.chunkname || short != c.id {
			t.Errorf("LoadChunk(%q): source %q, short_src %q", c.chunkname, src, short)
		}
		L.Push(fn)
		if err := L.PCall(0, 0, nil); err == nil || !strings.HasPrefix(err.Error(), c.id+":2: e") {
			t.Errorf("LoadChunk(%q): error %v", c.chunkname, err)
		}
	}
	// a dump keeps the chunk name, in its nested functions too
	if err := L.DoString(`
local f = loadstring("return function() end", "@orig.lua")
local g = loadstring(string.dump(f), "=other")
src1, src2 = debug.getinfo(g, "S").source, debug.getinfo(g(), "S").source`); err != nil {
		t.Fatal(err)
	}
	if s1, s2 := L.GetGlobal("src1").String(), L.GetGlobal("src2").String(); s1 != "@orig.lua" || s2 != "@orig.lua" {
		t.Errorf("dumped: sources %q, %q", s1, s2)
	}
}
