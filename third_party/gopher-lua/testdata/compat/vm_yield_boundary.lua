-- coroutine.yield across a Go (C) call boundary: pcall, metamethods, Go
-- functions calling Lua, generic-for iterators. Lua 5.1 raises "attempt to
-- yield across metamethod/C-call boundary"; gopher-lua used to resume the
-- wrong frame and crash (issue #545).
local function show(...)
  local t = {...}
  for i = 1, select("#", ...) do t[i] = tostring(t[i]) end
  print(table.concat(t, " "))
end

local co = coroutine.create(function()
  local ok, err = pcall(function() coroutine.yield(1) end)
  return ok, err
end)
show(coroutine.resume(co))
show(coroutine.status(co))

co = coroutine.create(function()
  return pcall(table.sort, {3, 1, 2}, function(x, y) coroutine.yield(); return x < y end)
end)
show(coroutine.resume(co))

co = coroutine.create(function()
  show(pcall(function() coroutine.yield() end))
  coroutine.yield("again")
  return "done"
end)
show(coroutine.resume(co))
show(coroutine.resume(co))

local gen = coroutine.wrap(function() coroutine.yield(1); return 2 end)
show(gen(), gen())

-- metamethods
local mt = {__index = function(t, k) return coroutine.yield(k) end,
            __add = function(a, b) return coroutine.yield("add") end}
co = coroutine.create(function() local t = setmetatable({}, mt); return t.foo end)
show(coroutine.resume(co))
co = coroutine.create(function() local t = setmetatable({}, mt); return t + 1 end)
show(coroutine.resume(co))

-- generic for iterator
co = coroutine.create(function()
  for k in function() return coroutine.yield("it") end do return k end
end)
show(coroutine.resume(co))
show(coroutine.status(co))

-- xpcall with a handler, then the coroutine goes on yielding normally
co = coroutine.create(function()
  show(xpcall(function() coroutine.yield() end, function(m) return "handled: " .. m end))
  local x = coroutine.yield("after xpcall")
  return x * 2
end)
show(coroutine.resume(co))
show(coroutine.resume(co, 21))

-- an error inside a pcall inside a coroutine resets the boundary count
co = coroutine.create(function()
  for i = 1, 3 do
    show(pcall(error, "e" .. i, 0))
    coroutine.yield(i)
  end
  return "end"
end)
for i = 1, 4 do show(coroutine.resume(co)) end

-- string.gsub with a yielding replacement function
co = coroutine.create(function() return string.gsub("abc", "%w", function(c) coroutine.yield(c) end) end)
show(coroutine.resume(co))

-- yield from the main chunk
show(pcall(coroutine.yield, 1))

-- tail-called yield and nested coroutines still work
local inner = coroutine.wrap(function(a) local b = coroutine.yield(a + 1); return b * 10 end)
co = coroutine.create(function(a)
  local x = inner(a)
  local y = coroutine.yield(x)
  return inner(y)
end)
show(coroutine.resume(co, 1))
show(coroutine.resume(co, 3))
