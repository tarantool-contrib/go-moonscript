-- debug.getinfo's name and namewhat are Lua 5.1's getfuncname: from the
-- instruction of the caller that called the function ("global", "local",
-- "method", "field", "upvalue"; no name and "" for a function called by a
-- Go function, a metamethod, a tail call or the main chunk; was gopher-
-- lua's name recorded at compile time, no namewhat).
local function show(tag)
  local info = debug.getinfo(2, "n")
  print(tag, info.name, info.namewhat)
end
function gf() show("global") end
local function lf() show("local") end
local t = {f = function() show("field") end}
function t:m() show("method") end
local up = function() show("upvalue") end
gf()
lf()
t.f()
t:m()
t["f"]()
local k = "f"
t[k]()
;(function() up() end)()
pcall(gf)
local mt = setmetatable({}, {__index = function() show("__index") end})
local _ = mt.x
local function tail() return gf() end
tail()
local function caller() local x = gf() return x end
caller()
for _ in function() show("generator") end do end
show("main")
local info = debug.getinfo(1, "n")
print("main chunk", info.name, info.namewhat)
print("level 0", debug.getinfo(0, "n").name, debug.getinfo(0, "n").namewhat)
local g = debug.getinfo
print("level 0 local", g(0, "n").name, g(0, "n").namewhat)
print("function", debug.getinfo(print, "n").name, debug.getinfo(print, "n").namewhat)
local co = coroutine.create(function() show("coroutine") end)
coroutine.resume(co)
local s = string
print("nested", (function() return debug.getinfo(1, "n").namewhat end)())
local obj = {inner = {fn = function() show("nested field") end}}
obj.inner.fn()
local mm = setmetatable({}, {__call = function() show("__call") end})
mm()
print(select(2, pcall(function() t.nofunc() end)))
