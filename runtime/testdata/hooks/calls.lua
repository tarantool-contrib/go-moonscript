-- call, return and line events of Lua and C functions, with what
-- debug.getinfo(2) reports for the hooked function
local events = {}
local function hook(event, line)
  local info = debug.getinfo(2, "Sl")
  events[#events + 1] = event .. " " .. tostring(line) .. " " ..
    info.short_src .. ":" .. info.currentline .. " " .. info.what
end

local function add(a, b)
  return a + b
end

local function twice(f, x)
  local y = f(x, x)
  return y * 2
end

local obj = {n = 1}
function obj:inc(k)
  self.n = self.n + k
  return self.n
end

debug.sethook(hook, "crl")
local x = add(1, 2)
local y = twice(add, x)
obj:inc(y)
local s = tostring(x) .. string.rep("-", 2)
local t = setmetatable({}, {__index = function(t, k) return k .. "!" end})
local v = t.foo
local n = select("#", 1, 2, 3)
debug.sethook()

for _, e in ipairs(events) do print(e) end
print(x, y, obj.n, s, v, n)
