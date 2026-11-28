package lua

// go-moonscript patch: LuaJIT's FFI numerals (1LL, 1ULL, 1i) in
// DialectLuaJIT. They are cdata, which the VM doesn't have: they parse, as
// with luajit, and evaluating one is an error (luajit evaluates them to
// cdata, so this can't be compared with its output).

import (
	"strings"
	"testing"
)

func TestCdataNumerals(t *testing.T) {
	const script = `
local function try(s)
  local f, err = loadstring(s, "s")
  if not f then return print("load", err) end
  print(pcall(f))
end
try("return 1LL")
try("return 0x10ULL")
try("return 1llu")
try("return 12.5i")
try("return 0b101I")
try("local a = 1\n\nreturn -1LL")
try("return 1LL + 1")
try("return {1LL}")
try("return {[1LL] = 1}")
try("if 1LL then return 1 end")
try("if not 1LL then return 1 end")
try("return 1LL and 2")
try("return false and 1LL, true or 1i")
try("for i = 1LL, 2 do end")
try("local x = 1LL")
try("local x; x = 1LL")
try("x = 1i")
try("return print(1LL)")
try("return 1LL .. ''")
try("return 1.5LL")
try("return 1e2LL")
try("return 1U")
-- a register above the call isn't clobbered: an upvalue keeps its value
local get
print(pcall(function()
  local a, b = 1, 2
  get = function() return b end
  a = 1LL
end))
print(get())
-- string.dump keeps the call
local f = loadstring("local x = 1\nreturn 2i", "=dumped")
print(pcall(loadstring(string.dump(f))))
print(pcall(loadstring(string.dump(f, true))))
-- the message handler sees the error
print(xpcall(function() return 1LL end, function(m) return "handled: " .. m end))
print(xpcall(function() return 1LL end, debug.traceback))
`
	L := NewState(Options{Dialect: DialectLuaJIT})
	defer L.Close()
	var out strings.Builder
	L.SetGlobal("print", L.NewFunction(func(L *LState) int {
		for i := 1; i <= L.GetTop(); i++ {
			if i > 1 {
				out.WriteByte('\t')
			}
			out.WriteString(L.ToStringMeta(L.Get(i)).String())
		}
		out.WriteByte('\n')
		return 0
	}))
	if err := L.DoString(script); err != nil {
		t.Fatal(err)
	}
	const want = `false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '0x10ULL' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '1llu' needs the FFI, which is not supported
false	[string "s"]:1: complex number literal '12.5i' needs the FFI, which is not supported
false	[string "s"]:1: complex number literal '0b101I' needs the FFI, which is not supported
false	[string "s"]:3: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
true	false	true
false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	[string "s"]:1: complex number literal '1i' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	[string "s"]:1: 64-bit integer literal '1LL' needs the FFI, which is not supported
load	[string "s"]:1: malformed number near '1.5LL'
load	[string "s"]:1: malformed number near '1e2LL'
load	[string "s"]:1: malformed number near '1U'
false	<string>:34: 64-bit integer literal '1LL' needs the FFI, which is not supported
2
false	dumped:2: complex number literal '2i' needs the FFI, which is not supported
false	dumped:2: complex number literal '2i' needs the FFI, which is not supported
false	handled: <string>:42: 64-bit integer literal '1LL' needs the FFI, which is not supported
false	<string>:43: 64-bit integer literal '1LL' needs the FFI, which is not supported
stack traceback:
	[G]: in function (anonymous)
	<string>:43: in function <<string>:43>
	[G]: in function 'xpcall'
	<string>:43: in main chunk
	[G]: ?
`
	if got := out.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
