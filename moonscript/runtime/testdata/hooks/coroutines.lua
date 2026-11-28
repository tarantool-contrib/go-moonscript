-- hooks are per coroutine: a coroutine created while a hook is set gets its
-- mask and count but no hook function; debug.sethook(co, ...) hooks it;
-- yielding and resuming
local events = {}
local function hook(event, line)
  local info = debug.getinfo(2, "Sl")
  events[#events + 1] = "main " .. event .. " " .. tostring(line) .. " " ..
    info.short_src .. ":" .. info.currentline
end
local function cohook(event, line)
  local info = debug.getinfo(2, "Sl")
  events[#events + 1] = "co " .. event .. " " .. tostring(line) .. " " ..
    info.short_src .. ":" .. info.currentline
end

local inside
local early = coroutine.create(function()
  return debug.gethook()
end)

debug.sethook(hook, "crl", 0)
local co1 = coroutine.create(function(a)
  local f, mask, count = debug.gethook()
  inside = tostring(f) .. " " .. mask .. " " .. count
  local b = coroutine.yield(a + 1)
  return b * 2
end)
local r1 = {coroutine.resume(co1, 1)}
local r2 = {coroutine.resume(co1, 5)}
debug.sethook()

local co2 = coroutine.create(function(x)
  local y = x + 1
  y = coroutine.yield(y)
  return y
end)
debug.sethook(co2, cohook, "crl")
local r3 = {coroutine.resume(co2, 1)}
local r4 = {coroutine.resume(co2, 10)}
local f2, m2 = debug.gethook(co2)
debug.sethook(co2)

local gen = coroutine.wrap(function()
  for i = 1, 2 do coroutine.yield(i) end
end)
debug.sethook(hook, "l")
local g1 = gen()
local g2 = gen()
debug.sethook()

-- a hook set on a suspended coroutine sees the return of the yield
local co3 = coroutine.create(function()
  coroutine.yield()
  local z = 1
  return z
end)
coroutine.resume(co3)
debug.sethook(co3, cohook, "r")
local r5 = {coroutine.resume(co3)}
debug.sethook(co3)

for _, e in ipairs(events) do print(e) end
print(inside)
print(coroutine.resume(early))
print(r1[1], r1[2], r2[1], r2[2], r3[1], r3[2], r4[1], r4[2])
print(f2 == cohook, m2, g1, g2, r5[1], r5[2])
