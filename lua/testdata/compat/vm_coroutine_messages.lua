-- Lua 5.1's coroutine.resume messages ("cannot resume dead coroutine",
-- "cannot resume running coroutine"; were "can not resume a dead thread",
-- "can not resume a running thread"), and coroutine.wrap's: a string error
-- gets the position of the wrap function's caller (none if a Go function
-- such as pcall called it).
local co = coroutine.create(function() end)
print(coroutine.resume(co))
print(coroutine.resume(co))
co = coroutine.create(function() return coroutine.resume(co) end)
print(coroutine.resume(co))

local w = coroutine.wrap(function() error("boom") end)
print(pcall(function()
  w()
end))
print(pcall(function()
  w()
end))
print(pcall(w))
w = coroutine.wrap(function() error("lvl0", 0) end)
print(pcall(function() local x = w() end))
local t = {}
w = coroutine.wrap(function() error(t) end)
print(select(2, pcall(function() local x = w() end)) == t)
local ws
ws = coroutine.wrap(function() return ws() end)
print(pcall(ws))
ws = coroutine.wrap(function() return pcall(function() return ws() end) end)
print(pcall(ws))
