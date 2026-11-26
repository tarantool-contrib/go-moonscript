-- Resuming a coroutine that is active further up the resume chain (status
-- "normal") fails as in Lua 5.1; gopher-lua re-entered it and the process
-- died of a Go stack overflow. coroutine.status reports "normal" for every
-- active coroutine, not only the direct resumer.
local a
local b = coroutine.create(function()
  print("a in b", coroutine.status(a))
  return coroutine.resume(a)
end)
a = coroutine.create(function() return coroutine.resume(b) end)
print(coroutine.resume(a))
print(coroutine.status(a), coroutine.status(b))

local c
a = coroutine.create(function()
  b = coroutine.create(function()
    c = coroutine.create(function()
      print("in c", coroutine.status(a), coroutine.status(b), coroutine.status(c))
      return coroutine.resume(a)
    end)
    return coroutine.resume(c)
  end)
  return coroutine.resume(b)
end)
print(coroutine.resume(a))
print(coroutine.status(a), coroutine.status(b), coroutine.status(c))

-- a wrapped coroutine re-entered through a child raises the error
local w
w = coroutine.wrap(function()
  local inner = coroutine.create(function()
    return pcall(function() return w() end)
  end)
  return coroutine.resume(inner)
end)
print(w())

-- a suspended coroutine still resumes normally afterwards
local s = coroutine.create(function() coroutine.yield(1) return 2 end)
print(coroutine.resume(s))
print(coroutine.status(s))
print(coroutine.resume(s))
print(coroutine.status(s))
