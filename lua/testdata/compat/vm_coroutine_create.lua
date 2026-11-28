-- coroutine.create and coroutine.wrap take a Lua function only, as in Lua
-- 5.1 (LuaJIT also accepts a C function).
local function try(f, ...)
  local ok, err = pcall(f, ...)
  print(ok, string.find(err, "Lua function expected", 1, true) ~= nil)
end
try(coroutine.create, print)
try(coroutine.wrap, string.rep)
try(coroutine.create, 1)
try(coroutine.wrap)
print(type(coroutine.create(function() end)))
print(type(coroutine.wrap(function() end)))
