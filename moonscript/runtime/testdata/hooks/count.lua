-- count events, alone and with line events, and a count hook that stops a
-- runaway loop. The hooks are C functions (print) where the count matters:
-- instructions a Lua hook runs count too, and their number depends on the
-- compiler.
local events = {}
local function hook(event, line)
  events[#events + 1] = event .. " " .. tostring(line)
end

local function work()
  local s = 0
  for i = 1, 20 do
    s = s + i
  end
  return s
end

debug.sethook(print, "", 10)
work()
debug.sethook()

debug.sethook(print, "l", 7)
work()
debug.sethook()

debug.sethook(print, "", 1)
local a = 1
local b = a + 1
debug.sethook()

debug.sethook(hook, "", 1000)
work()
debug.sethook()

local steps = 0
local ok, err = pcall(function()
  debug.sethook(function()
    steps = steps + 1
    if steps == 5 then error("too long", 0) end
  end, "", 100)
  local i = 0
  while true do i = i + 1 end
end)
debug.sethook()

print(#events)
print(ok, err, steps)
