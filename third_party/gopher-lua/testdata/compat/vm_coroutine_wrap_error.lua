-- A wrapped coroutine that raises an error is dead and the resumer is the
-- running thread again; gopher-lua left it running, so calling it again
-- re-entered its failed frame.
local n = 0
local w = coroutine.wrap(function() n = n + 1; error("boom") end)
print(pcall(w))
local s = coroutine.create(function() coroutine.yield(1) end)
print(coroutine.resume(s))
local ok, err = pcall(w)
print(ok, string.find(err, "dead", 1, true) ~= nil)
print(n)
print(coroutine.running())

local outer = coroutine.create(function()
  local inner = coroutine.wrap(function() error("inner") end)
  local ok2 = pcall(inner)
  coroutine.yield(ok2, coroutine.running() ~= nil)
  return "done"
end)
print(coroutine.resume(outer))
print(coroutine.status(outer))
print(coroutine.resume(outer))

local t = {}
local w2 = coroutine.wrap(function() error(t) end)
local ok3, e3 = pcall(w2)
print(ok3, e3 == t)
ok3, e3 = pcall(w2)
print(ok3, string.find(e3, "dead", 1, true) ~= nil)

local outer2 = coroutine.wrap(function()
  local w3 = coroutine.wrap(function() error("x") end)
  print("in outer2", pcall(w3))
  print("in outer2", coroutine.running() ~= nil)
  coroutine.yield(1)
  return 2
end)
print(outer2())
print(outer2())
