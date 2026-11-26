-- The values a coroutine is resumed with are the results of its yield:
-- missing ones are nil (gopher-lua left stale or invalid registers there and
-- crashed), extra ones are dropped. A yield that is the body's tail call
-- leaves no frame: the next resume returns the values and the coroutine is
-- dead (gopher-lua crashed).
local co = coroutine.create(function()
  local x, y, z = 1, 2, 3
  local a, b, c = coroutine.yield()
  print(a, b, c)
  local t = {coroutine.yield()}
  print(#t, t[1])
  local p, q = 7, 8
  p, q = coroutine.yield()
  print(p, q)
  local u, v = coroutine.yield()
  print(u, v)
end)
print("R", coroutine.resume(co))
print("R", coroutine.resume(co, "one"))
print("R", coroutine.resume(co))
print("R", coroutine.resume(co, "x"))
print("R", coroutine.resume(co, 1, 2, 3, 4))
print(coroutine.status(co))

co = coroutine.create(function() return coroutine.yield("tail") end)
print(coroutine.resume(co))
print(coroutine.status(co))
print(coroutine.resume(co, "back", 2))
print(coroutine.status(co))

local function f(...) return coroutine.yield(...) end
local w = coroutine.wrap(function(...)
  local a, b = f(...)
  print("f", a, b)
  local c = f()
  print(c)
  return "fin"
end)
print(w(1, 2))
print(w("x"))
print(w(4, 5, 6))

co = coroutine.create(function(a)
  local x, y = coroutine.yield(a)
  return x, y, select("#", coroutine.yield())
end)
print(coroutine.resume(co, 1))
print(coroutine.resume(co, 1, 2, 3))
print(coroutine.resume(co, nil, nil, nil))
print(coroutine.status(co))

-- a tail-called yield inside a generator used by a for loop
local gen = coroutine.wrap(function()
  for i = 1, 3 do coroutine.yield(i) end
  return coroutine.yield(nil)
end)
for i in gen do print("gen", i) end
