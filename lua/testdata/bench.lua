-- CPU-bound script for BenchmarkVM: calls, loops, arithmetic, tables,
-- strings, closures and method calls, with no hook set.
local function fib(n)
  if n < 2 then return n end
  return fib(n - 1) + fib(n - 2)
end

local t = {}
for i = 1, 100000 do t[i] = i * 2 end
local s = 0
for i = 1, #t do s = s + t[i] % 7 end

local parts = {}
for i = 1, 10000 do parts[#parts + 1] = tostring(i) end
local str = table.concat(parts, ",")
local n = 0
for w in string.gmatch(str, "%d+") do n = n + #w end

local Point = {}
Point.__index = Point
function Point.new(x, y) return setmetatable({x = x, y = y}, Point) end
function Point:add(o) return Point.new(self.x + o.x, self.y + o.y) end
local p = Point.new(0, 0)
for i = 1, 50000 do p = p:add(Point.new(1, i)) end

local function counter()
  local c = 0
  return function() c = c + 1; return c end
end
local inc = counter()
for i = 1, 100000 do inc() end

local x = 0
local i = 0
while i < 300000 do
  i = i + 1
  if i % 3 == 0 then x = x + 1 elseif i % 3 == 1 then x = x - 1 else x = x * 1 end
end

return fib(22) + s + n + p.x + inc() + x
