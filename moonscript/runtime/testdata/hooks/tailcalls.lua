-- tail calls: "tail return" events, tail calls of C functions, and the
-- levels lost to tail calls (debug.getinfo reports "(tail call)")
local events = {}
local function hook(event, line)
  local info = debug.getinfo(2, "Sl")
  events[#events + 1] = event .. " " .. tostring(line) .. " " ..
    info.short_src .. ":" .. info.currentline .. " " .. info.what
end

local function count(n)
  if n == 0 then return "done" end
  return count(n - 1)
end

local function c_tail(x)
  return tostring(x)
end

local function via(f, ...)
  return f(...)
end

local function where()
  local here = debug.getinfo(1, "S").what
  local up = debug.getinfo(2, "S")
  return here .. " " .. up.what .. " " .. up.short_src .. " " .. tostring(up.currentline)
end

local function tail_where()
  return where()
end

debug.sethook(hook, "cr")
local a = count(2)
local b = c_tail(42)
local c = via(count, 1)
local d = via(c_tail, 7)
debug.sethook()

-- a hook reached through a tail call sees the lost level, as moon's
-- coverage hook (a bound method wrapper) does
local seen = {}
local target = {}
function target:record(event, line)
  local info = debug.getinfo(2, "S")
  seen[#seen + 1] = event .. " " .. info.short_src .. " " .. info.what
end
local bound = function(...) return target:record(...) end
debug.sethook(bound, "l")
local z = 1
debug.sethook()

for _, e in ipairs(events) do print(e) end
for _, e in ipairs(seen) do print(e) end
print(a, b, c, d, tail_where())
