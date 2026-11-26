-- pcall, error and xpcall: no return events for frames an error unwinds;
-- hooks are off while a hook runs; an error raised by a hook
local events = {}
local function hook(event, line)
  local info = debug.getinfo(2, "Sl")
  events[#events + 1] = event .. " " .. tostring(line) .. " " ..
    info.short_src .. ":" .. info.currentline
end

local function fail(msg)
  error(msg)
end

local function deep(n)
  if n == 0 then fail("deep") end
  local r = deep(n - 1)
  return r
end

local function handler(e)
  return "handled: " .. e
end

debug.sethook(hook, "crl")
local ok1, e1 = pcall(fail, "boom")
local ok2, e2 = pcall(deep, 2)
local ok3, e3 = xpcall(function() fail("x") end, handler)
local ok4, e4 = pcall(error)
debug.sethook()

-- a hook that calls functions and loops runs no hooks itself
local inner = {}
local function busy(event, line)
  local n = 0
  for i = 1, 3 do n = n + i end
  inner[#inner + 1] = event .. " " .. tostring(line) .. " " .. tostring(n)
  local _, _, count = debug.gethook()
  inner[#inner + 1] = "count " .. count
end
debug.sethook(busy, "crl")
local q = 1
debug.sethook()

-- an error in a hook propagates from the hooked line; the hook stays set
-- and runs again afterwards
local fired, calls = false, 0
local function bomb(event, line)
  calls = calls + 1
  if not fired then
    fired = true
    error("hook error at " .. line, 0)
  end
end
local function target()
  local a = 1
  local b = 2
  return a + b
end
local ok5, e5 = pcall(function()
  debug.sethook(bomb, "l")
  return target()
end)
local still = debug.gethook() == bomb
local w = 1
debug.sethook()
local after = debug.gethook()

for _, e in ipairs(events) do print(e) end
for _, e in ipairs(inner) do print(e) end
print(ok1, e1)
print(ok2, e2)
print(ok3, e3)
print(ok4, e4)
print(ok5, e5, still, calls, after)
