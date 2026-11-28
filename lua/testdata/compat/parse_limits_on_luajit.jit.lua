-- LuaJIT's limits, which are those of its parser and of its code generator
-- (lj_parse.c): 65476 locals, gotos and labels declared in a function and
-- the functions enclosing it (LJ_MAX_VSTACK), jumps over at most 32767
-- instructions, frames of fewer than 250 slots (LJ_MAX_SLOTS, a call takes
-- two more for its frame), 65536 constants of each kind (numbers, and
-- strings, functions and tables), returns before the first closure that a
-- jump of 32767 instructions reaches (the fixup of returns). Which code
-- reaches them depends on LuaJIT's bytecode, which the parser emits under
-- DialectLuaJIT. parse_limits.lua has Lua 5.1's.
local r = string.rep
local function try(name, src)
  local f, err = loadstring(src, "=src")
  if f then
    print(name, "ok")
  else
    print(name, (err:gsub("^[^:]*:", "")))
  end
end
local function locals(n, init)
  local b = {}
  for i = 0, n - 1 do b[#b + 1] = "local v" .. i .. init .. "\n" end
  return table.concat(b)
end
local function call(n) return "consume(" .. r("1,", n - 1) .. "1)\n" end
local main = "-- main chunk\n"
-- registers
try("call 54", main .. locals(195, " = 1") .. call(54))
try("call 53", main .. locals(195, " = 1") .. call(53))
try("call 52", main .. locals(195, " = 1") .. call(52))
try("call later", main .. locals(195, " = 1") .. call(20) .. "\n" .. call(60))
try("call lines", main .. locals(195, " = 1") .. "consume(\n" .. r("1,\n", 59) .. "1\n)\n")
try("table", main .. locals(195, " = 1") .. "local t = {" .. r("1,", 300) .. "}\n")
try("concat", main .. locals(195, " = 1") .. "local s = " .. r("v1 .. ", 60) .. "v1\n")
try("return", main .. locals(195, " = 1") .. "return " .. r("v1, ", 60) .. "v1\n")
try("args 247", "f(" .. r("a,", 246) .. "a)")
try("args 248", "f(" .. r("a,", 247) .. "a)")
try("method", main .. locals(195, " = 1") .. "o:m(" .. r("1, ", 50) .. "1)\n")
try("before a syntax error", main .. locals(195, " = 1") .. call(60) .. "x = = 1\n")
-- jumps
local function calls(n) return r("f() ", n) end -- 2 instructions each
try("while 16381", "while x do " .. calls(16381) .. "end")
try("while 16382", "while x do " .. calls(16382) .. "end")
try("if 16384", "if x then " .. calls(16384) .. "end")
try("if else 16384", "if x then " .. calls(16384) .. "else f() end")
try("repeat 16383", "repeat " .. calls(16383) .. "until x")
try("goto 16384", "goto a " .. calls(16384) .. "::a::")
try("break 16380", "while x do if y then break end " .. calls(16380) .. "end")
try("and 10922", "x = y and f(" .. r("g() + ", 10922) .. "1)")
-- the fixup of returns before the first closure
try("fixup 16382", "if x then return end " .. calls(16382) .. " local function g() end")
try("fixup 16383", "if x then return end " .. calls(16383) .. " local function g() end")
-- constants
local function statements(n, format)
  local b = {}
  for i = 0, n - 1 do b[#b + 1] = string.format(format, i) end
  return table.concat(b)
end
try("numbers 65536", statements(65536, "x=%d.5 "))
try("numbers 65537", statements(65537, "x=%d.5 "))
try("strings 65536", statements(65536, "x='%d' "))
try("functions 65536", "local function f() " .. r("x=function() end ", 65536) .. "end")
-- the variable stack
local function vars(n)
  local b = {}
  while n > 0 do
    b[#b + 1] = "do local a" .. r(",a", math.min(n, 100) - 1) .. " end\n"
    n = n - 100
  end
  return table.concat(b)
end
try("vstack 65476", vars(65476))
try("vstack 65477", vars(65477))
try("vstack break", vars(65475) .. "while x do break end")
try("vstack function", vars(65000) .. "local function f() " .. vars(476) .. " end")
try("vstack after function", "local function f() " .. vars(65475) .. " end " .. vars(65475))
