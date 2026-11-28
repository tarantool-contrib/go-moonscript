-- The implicit local "arg" of a vararg function: Lua 5.1 (LUA_COMPAT_VARARG)
-- declares it, a table of the extra arguments when the function does not
-- use "...", nil when it does; LuaJIT 2.1 declares none, "arg" is the
-- global there (parse_vararg_arg.lua: the same script for lua5.1).
arg = "global"
local function f(...) return type(arg) end
print("no ...", f(1, 2))
local function g(a, ...) local t = arg return select("#", ...), type(t), type(t) == "table" and t.n end
print("extra", g(1, 2, 3))
local function g2(a, ...) local t = arg return type(t), type(t) == "table" and t.n, type(t) == "table" and t[2] end
print("table", g2(1, 2, 3))
local function h(...) local a = ... return type(arg) end
print("with ...", h(1, 2))
print("assign", (function(...) arg = 5 return arg end)(1), arg)
arg = "global"
local function names(...)
  local n, i = {}, 1
  while true do
    local k = debug.getlocal(1, i)
    if not k then break end
    n[#n + 1] = k
    i = i + 1
  end
  return table.concat(n, ",")
end
print("locals", names(1, 2))
local function nested(...) return function() return type(arg) end end
print("upvalue", nested(1)())
local t = {}
function t:m(...) return type(arg), select("#", ...) end
print("method", t:m(1, 2))
print("main chunk", type(arg))
print("loaded chunk", loadstring("return type(arg), select('#', ...)")(1, 2))
