-- table constructors: positional items around the 50-item flushes of SETLIST
local items = {}
for i = 1, 50 do items[i] = "v" .. i end
local list = table.concat(items, ",")
local env = {}
for i = 1, 50 do env["v" .. i] = i end
setmetatable(_G, {__index = env})
local function show(src)
  local t = assert(loadstring("return " .. src))(7, 8)
  local n = 0
  for k in pairs(t) do n = n + 1 end
  print(#t, n, t[1], t[2], t[50], t[51], t[52], t.x, t.y)
end
show("{" .. list .. ", x = 'X'}")
show("{" .. list .. ", x = 'X', y = 'Y'}")
show("{" .. list .. ", ['x'] = 'X', [1] = 'one'}")
show("{x = 'X', " .. list .. "}")
show("{" .. list .. ", ...}")
show("{" .. list .. ", x = 'X', ...}")
show("{" .. list .. ", (...)}")
show("{" .. list .. ", 51}")
show("{" .. list .. ", 51, x = 'X'}")
show("{" .. list .. ", 51, [2] = 'two'}")
show("{v1, v2, x = ...}")
show("{v1, v2, [3] = 'three', x = ...}")
setmetatable(_G, nil)

local function f() return "a", "b", "c" end
local t = {1, 2, x = f()}
local n = 0
for k, v in pairs(t) do n = n + 1 end
print(#t, n, t[1], t[2], t[3], t.x)
local function g(...)
  local t = {10, x = ...}
  local n = 0
  for k, v in pairs(t) do n = n + 1 end
  return #t, n, t[1], t[2], t.x
end
print(g(5, 6, 7))
local function h(...) return {...} end
print(#h(1, 2, 3), #h())
