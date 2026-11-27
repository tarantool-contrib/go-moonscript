package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	lua "github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua"

	"github.com/tarantool-contrib/go-moonscript/runtime/gen"
)

// TestGenerated checks that the embedded Lua (and cmd/moon/moon.lua) is
// what `go generate ./runtime` produces from the .moon sources.
func TestGenerated(t *testing.T) {
	pairs, err := gen.Sources(os.DirFS(".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) < len(embedded) {
		t.Fatalf("%d sources for %d embedded modules", len(pairs), len(embedded))
	}
	for _, p := range pairs {
		src, err := os.ReadFile(filepath.Join("..", p.Src))
		if err != nil {
			t.Fatal(err)
		}
		want, err := gen.Compile(string(src))
		if err != nil {
			t.Fatalf("%s: %v", p.Src, err)
		}
		got, err := os.ReadFile(filepath.Join("..", p.Out))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s is out of date: run go generate ./runtime", p.Out)
		}
	}
	for name, file := range embedded {
		if _, err := luaFiles.ReadFile(file); err != nil {
			t.Errorf("module %s: %v", name, err)
		}
	}
}

// run runs Lua code in a fresh state and returns what it printed through
// the out function.
func run(t *testing.T, code string) string {
	t.Helper()
	L := NewState()
	defer L.Close()
	var out []string
	L.SetGlobal("out", L.NewFunction(func(L *lua.LState) int {
		var parts []string
		for i := 1; i <= L.GetTop(); i++ {
			parts = append(parts, L.ToStringMeta(L.Get(i)).String())
		}
		out = append(out, strings.Join(parts, "\t"))
		return 0
	}))
	fn, err := LoadLua(L, code, "=test")
	if err != nil {
		t.Fatal(err)
	}
	L.Push(fn)
	if err := L.PCall(0, 0, nil); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, "\n")
}

func TestModules(t *testing.T) {
	names := Modules()
	sort.Strings(names)
	var code strings.Builder
	for _, name := range names {
		code.WriteString("out(type(require(\"" + name + "\")))\n")
	}
	got := run(t, code.String())
	if want := strings.Repeat("table\n", len(names)-1) + "table"; got != want {
		t.Errorf("require of %v:\n%s", names, got)
	}
	// moonscript (init) inserts the loader; moon.all exports into _G
	got = run(t, `
local base = require "moonscript.base"
out(package.moonpath)
require "moonscript"
out(package.loaders[2] == base.moon_loader, package.moonpath == base.create_moonpath(package.path))
require "moon.all"
out(type(p), type(fold), p == require("moon").p)
`)
	if want := "nil\ntrue\ttrue\nfunction\tfunction\ttrue"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestChunkID(t *testing.T) {
	// values from PUC Lua 5.1.5
	for _, c := range []struct{ in, want string }{
		{"=(command line)", "(command line)"},
		{"=", ""},
		{"=" + strings.Repeat("n", 70), strings.Repeat("n", 59)},
		{"@foo.moon", "foo.moon"},
		{"@" + strings.Repeat("d/", 40) + "f.lua", "..." + strings.Repeat("/d", 23) + "/f.lua"},
		{"error('e')", `[string "error('e')"]`},
		{strings.Repeat("s", 50), `[string "` + strings.Repeat("s", 43) + `..."]`},
		{"line one\nline two", `[string "line one..."]`},
	} {
		if got := ChunkID(c.in); got != c.want {
			t.Errorf("ChunkID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatTraceback(t *testing.T) {
	in := "boom\nstack traceback:\n\t[G]: in function 'error'\n\tx.moon:3: in function <x.moon:1>\n\t(tailcall): ?\n\tlib.moon:4: in function <lib.moon:0>\n\t[G]: in function (anonymous)\n\t[G]: ?"
	want := "boom\nstack traceback:\n\t[C]: in function 'error'\n\tx.moon:3: in function <x.moon:1>\n\t(tail call): ?\n\tlib.moon:4: in main chunk\n\t[C]: ?\n\t[C]: ?"
	if got := FormatTraceback(in); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestCompat checks the Lua 5.1 compatibility patches against PUC Lua
// 5.1.5's output for the same code.
func TestCompat(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.lua")
	for _, c := range []struct{ name, code, want string }{
		{"loadstring syntax error", `out(loadstring("x = = 1"))`, `nil	[string "x = = 1"]:1: unexpected symbol near '='`},
		{"loadstring syntax error message", `out(loadstring("(f)", "=input"))`, `nil	input:1: syntax error near '<eof>'`},
		{"loadstring error without a position", `out(pcall(loadstring, string.rep("do local a end ", 32768), "=input"))`, "true\tnil\ttoo many local variables"},
		{"loadstring chunk names", `
local function msg(f) local ok, e = pcall(f); return e end
out(msg(loadstring("error('e')")))
out(msg(loadstring("error('e')", "@f.moon")))
out(msg(loadstring("error('e')", "=name")))`, "[string \"error('e')\"]:1: e\nf.moon:1: e\nname:1: e"},
		{"load", `out(pcall(load(function() return nil end))); local parts = {"return ", "1 + ", "1"}; local i = 0
out(load(function() i = i + 1; return parts[i] end)())`, "true\n2"},
		{"loadfile missing", `out(loadfile("` + missing + `"))`, "nil\tcannot open " + missing + ": No such file or directory"},
		{"dofile missing", `out(pcall(dofile, "` + missing + `"))`, "false\tcannot open " + missing + ": No such file or directory"},
		{"io.open missing", `out(io.open("` + missing + `"))`, "nil\t" + missing + ": No such file or directory\t2"},
		{"numbers", `out(tostring(1/3), tostring(1e15), tostring(2^53), tostring(-1/0), tostring(123456789012345), tostring(10/2))`, "0.33333333333333\t1e+15\t9.007199254741e+15\t-inf\t1.2345678901234e+14\t5"},
		{"error levels", `
local function msg(f) local ok, e = pcall(f); return e end
local function lvl() error("two", 2) end
local function caller() lvl(); return 1 end
out(msg(caller))
out(msg(function() return lvl() end))
out(msg(function() error(42) end))
out(msg(function() error("zero", 0) end))
out(msg(function() error({}) end) ~= nil)`, "test:4: two\ntwo\ntest:7: 42\nzero\ntrue"},
		{"require", `local ok, e = pcall(require, "no.such.module")
out(ok, e:match("^[^\n]*"))
out(e:match("\n\tno field package.preload%['no.such.module'%]\n\tno file '%./no/such/module%.lua'\n") ~= nil)
package.preload.loop = function() return require "loop" end
out(pcall(require, "loop"))`, "false\tmodule 'no.such.module' not found:\ntrue\nfalse\ttest:4: loop or previous error loading module 'loop'"},
		{"string.format %q", `out(string.format("%q|%5.1f|%s|%%|%d", "a\0b\r\n\"\\", 2.5, "x", 3))`, "\"a\\000b\\r\\\n\\\"\\\\\"|  2.5|x|%|3"},
		{"debug.getinfo", `local info = debug.getinfo(1, "S"); out(info.source, info.short_src, info.what)
out(debug.getinfo(print).what, debug.getinfo(print).source)
local f = loadstring("return debug.getinfo(1, 'S')", "@x.lua"); local i = f(); out(i.source, i.short_src, i.what)`, "=test\ttest\tmain\nC\t=[C]\n@x.lua\tx.lua\tmain"},
		{"upvalues survive a caught error", `
local x = 1
local function get() return x end
pcall(error, "e")
x = 2
local f
pcall(function() local y = 5; f = function() return y end; error("e") end)
local a, b, c = 10, 20, 30
out(get(), f())
local ok = xpcall(function() pcall(error, "x"); local w = nil; return w.x end, function(m) x = 99; return m end)
out(ok, x)`, "2\t5\nfalse\t99"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := run(t, c.code)
			if got != c.want {
				t.Errorf("got\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

func TestGoAPI(t *testing.T) {
	L := NewState()
	defer L.Close()

	fn, err := LoadString(L, "x = 1\nerror 'boom'\n", "@fake.moon", Options{})
	if err != nil {
		t.Fatal(err)
	}
	lt, err := LineTable(L, "@fake.moon")
	if err != nil || len(lt) == 0 {
		t.Fatalf("line table %v, %v", lt, err)
	}
	L.Push(fn)
	perr := L.PCall(0, 0, nil)
	if perr == nil {
		t.Fatal("no error")
	}
	msg := apiErrorObject(t, perr).String()
	if msg != "fake.moon:2: boom" {
		t.Errorf("error %q", msg)
	}
	if _, err := LoadString(L, "x = = 1", "", Options{}); err == nil || !strings.HasPrefix(err.Error(), "Failed to parse") && !strings.HasPrefix(err.Error(), "failed to parse") {
		t.Errorf("syntax error: %v", err)
	}
	if _, err := LoadFile(L, filepath.Join(t.TempDir(), "missing.moon"), Options{}); err == nil || !strings.Contains(err.Error(), "No such file or directory") {
		t.Errorf("missing file: %v", err)
	}

	// a short relative path: Lua shortens long chunk names in positions
	dir := t.TempDir()
	t.Chdir(dir)
	path := "m.moon"
	if err := os.WriteFile(path, []byte("a = 1\n\nerror \"line three\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fn, err = LoadFile(L, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	lt, _ = LineTable(L, "@"+path)
	L.Push(fn)
	perr = L.PCall(0, 0, nil)
	luaMsg := apiErrorObject(t, perr).String()
	luaLine := 0
	if i := strings.Index(luaMsg, ".moon:"); i >= 0 {
		rest := luaMsg[i+len(".moon:"):]
		for _, r := range rest {
			if r < '0' || r > '9' {
				break
			}
			luaLine = luaLine*10 + int(r-'0')
		}
	}
	line, ok, err := ReverseLineNumber(L, path, lt, luaLine)
	if err != nil || !ok || line != 3 {
		t.Errorf("ReverseLineNumber = %d, %v, %v", line, ok, err)
	}

	tb := "stack traceback:\n\t[C]: in function 'error'\n\t" + path + ":" + itoa(luaLine) + ": in function 'moonscript_chunk'\n\tmoon:96: in main chunk"
	truncated, err := TruncateTraceback(L, tb, "")
	if err != nil || strings.Contains(truncated, "moon:96") || !strings.HasSuffix(truncated, "in main chunk") {
		t.Errorf("TruncateTraceback = %q, %v", truncated, err)
	}
	rewritten, ok, err := RewriteTraceback(L, truncated, luaMsg)
	want := "moon: " + path + ":3: (" + itoa(luaLine) + ") line three\nstack traceback:\n\t[C]: in function 'error'\n\t" + path + ":3: (" + itoa(luaLine) + ") in main chunk"
	if err != nil || !ok || rewritten != want {
		t.Errorf("RewriteTraceback = %q, %v, %v\nwant %q", rewritten, ok, err, want)
	}
	if _, ok, _ := RewriteTraceback(L, "no traceback", "x"); ok {
		t.Error("RewriteTraceback accepted a non-traceback")
	}

	mp, err := CreateMoonpath(L, "./?.lua;/a/?/init.lua;/b/?.so")
	if err != nil || mp != "./?.moon;/a/?/init.moon" {
		t.Errorf("CreateMoonpath = %q, %v", mp, err)
	}

	// the .moon loader
	if err := os.WriteFile(filepath.Join(dir, "mod.moon"), []byte("{ value: ... }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ins, err := InsertLoader(L); err != nil || !ins {
		t.Fatalf("InsertLoader = %v, %v", ins, err)
	}
	if ins, _ := InsertLoader(L); ins {
		t.Error("second InsertLoader inserted again")
	}
	L.SetField(L.GetGlobal("package"), "moonpath", lua.LString(filepath.Join(dir, "?.moon")))
	if err := L.DoString(`assert(require("mod").value == "mod")`); err != nil {
		t.Error(err)
	}
	if rm, err := RemoveLoader(L); err != nil || !rm {
		t.Errorf("RemoveLoader = %v, %v", rm, err)
	}
	if rm, _ := RemoveLoader(L); rm {
		t.Error("second RemoveLoader removed again")
	}

	res, err := DoFile(L, filepath.Join(dir, "mod.moon"))
	if err != nil || len(res) != 1 {
		t.Fatalf("DoFile = %v, %v", res, err)
	}
}

func itoa(n int) string {
	return strings.TrimSpace(lua.LNumber(n).String())
}

// apiErrorObject is the Lua error value of a failed PCall.
func apiErrorObject(t *testing.T, err error) lua.LValue {
	t.Helper()
	var ae *lua.ApiError
	if !errors.As(err, &ae) {
		t.Fatalf("not a Lua error: %v", err)
	}
	return ae.Object
}
