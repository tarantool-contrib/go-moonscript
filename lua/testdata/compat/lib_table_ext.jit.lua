-- LuaJIT 2.1: table.move, table.new (require "table.new") and table.clear
-- (require "table.clear"); no table.setn
local function show(t, n)
  local r = {}
  for i = 1, n do r[i] = tostring(t[i]) end
  return table.concat(r, ",")
end
print(type(table.move), table.setn)
print(show(table.move({1, 2, 3, 4, 5}, 1, 4, 2), 5), show(table.move({1, 2, 3, 4, 5}, 2, 5, 1), 5))
print(show(table.move({1, 2, 3}, 1, 3, 1, {}), 3), show(table.move({1, 2, 3}, 2, 3, 5), 6), show(table.move({1, 2, 3}, 3, 1, 1), 3))
-- raw accesses, int32 keys
local function rshow(t)
  local r = {}
  for i = -3, 8 do r[#r + 1] = tostring(rawget(t, i)) end
  return table.concat(r, ",")
end
local log = {}
local src = setmetatable({5}, {__index = function(_, k) log[#log + 1] = "get" .. k; return k * 10 end})
local dst = setmetatable({}, {__newindex = function(t, k, v) log[#log + 1] = k .. "=" .. tostring(v); rawset(t, k, v) end})
print(table.move(src, 1, 3, 4, dst) == dst, table.concat(log, " "), rshow(dst))
print(rshow(table.move({1, 2, 3}, 1.5, 3, 1)), rshow(table.move({1, 2, 3}, 1.7, 3, 1)), rshow(table.move({1, 2, 3}, -1.5, 1, 1)))
print(rshow(table.move({1, 2, 3}, 1, 2, -1.5)), rshow(table.move({1, 2, 3}, 1, 2, 2.5)), rshow(table.move({1, 2, 3}, 1, 2.5, 2)))
print(rshow(table.move({1, 2, 3}, "1", "2", "3")), rshow(table.move({1, 2, 3}, 1, 2, 0/0)), rshow(table.move({1, 2, 3}, 0/0, 2, 1)))
print(rshow(table.move({1, 2, 3}, 2^32 + 1, 2^32 + 2, 1)))
local a = {1, 2, 3}
print(table.move(a, 1, 3, 1) == a, select("#", table.move(a, 1, 0, 1)))
print(pcall(table.move, {}, 1, 2), (select(2, pcall(table.move, "x", 1, 2, 3)):match("table expected, got string")))
print((select(2, pcall(table.move, {}, 1, 2, 3, 5)):match("table expected, got number")), (select(2, pcall(table.move, {}, "a", 2, 3)):match("number expected, got string")))
print(package.preload["table.new"] ~= nil, package.preload["table.clear"] ~= nil, package.loaded["table.new"])
local new = require "table.new"
print(type(new), package.loaded["table.new"] == new, require("table.new") == new)
for _, s in ipairs({{0, 0}, {10, 0}, {0, 10}, {100, 100}, {-1, 0}, {1.5, 2.5}, {"3", "4"}}) do
  local t = new(s[1], s[2])
  print(type(t), next(t), #t)
end
print(pcall(new, -1, -1), pcall(new, 1, 2^40), pcall(new, 2^27 + 1, 0), pcall(new, 0, 2^26 + 1))
print((select(2, pcall(new, 1)):match("number expected")))
local t = new(4, 0)
for i = 1, 10 do t[i] = i end
print(#t, t[10])
local clear = require "table.clear"
print(type(clear), package.loaded["table.clear"] == clear)
local c = {1, 2, 3, x = 1, y = {}, [true] = false}
print(select("#", clear(c)), next(c), #c)
c[1], c.z = "a", "b"
print(c[1], c.z, #c)
local m = setmetatable({1, 2}, {__newindex = error, __index = function() return "idx" end})
clear(m)
print(rawget(m, 1), m[1], getmetatable(m) ~= nil)
print((pcall(clear)), (pcall(clear, 1)), (select(2, pcall(clear, 1)):match("table expected, got number")))
