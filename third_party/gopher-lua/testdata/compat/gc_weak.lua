-- weak tables (__mode "k", "v", "kv") as Lua 5.1's collector clears them
local function count(t)
  local n = 0
  for _ in pairs(t) do n = n + 1 end
  return n
end

-- weak keys: collectable keys go, strings, numbers and booleans stay
local k = setmetatable({}, {__mode = "k"})
local keep = {}
for i = 1, 10 do k[{}] = i end
k[keep] = "kept"
k["str"] = {}
k[1] = {}
k[true] = {}
k[function() end] = 1
collectgarbage()
print("k", count(k), k[keep], type(k.str), type(k[1]), type(k[true]))

-- weak values: collectable values go, in the array and the hash parts
local v = setmetatable({}, {__mode = "v"})
for i = 1, 10 do v[i] = {} end
for i = 1, 5 do v["x" .. i] = function() end end
v[11] = keep
v.s = string.rep("a", 30)
v.n = 42
v[keep] = true
collectgarbage()
print("v", count(v), v[11] == keep, v.s, v.n, v[keep], #v)

-- both
local kv = setmetatable({}, {__mode = "kv"})
kv[keep] = {}
kv[{}] = keep
kv[1] = keep
kv[2] = {}
kv.x = "x"
collectgarbage()
print("kv", count(kv), kv[1] == keep, kv.x)

-- a value that refers to its weak key keeps it (no ephemerons in 5.1)
local e = setmetatable({}, {__mode = "k"})
do
  local key = {}
  e[key] = {ref = key}
  local key2 = {}
  e[key2] = 1
end
collectgarbage()
print("cycle", count(e))

-- the mode is read at each collection
local mt = {__mode = "k"}
local m = setmetatable({}, mt)
m[1] = {}
m[{}] = 1
mt.__mode = "v"
collectgarbage()
print("mode v", count(m), m[1])
m[1] = {}
m[{}] = 1
mt.__mode = nil
collectgarbage()
print("no mode", count(m))
mt.__mode = "k"
collectgarbage()
print("mode k", count(m))
mt.__mode = 1 -- not a string: strong
m[{}] = 1
collectgarbage()
print("mode 1", count(m))

-- next and pairs go on over entries cleared during the traversal
local w = setmetatable({}, {__mode = "v"})
for i = 1, 20 do w["k" .. i] = (i % 2 == 0) and keep or {} end
local seen = 0
for key, val in pairs(w) do
  seen = seen + 1
  if seen == 3 then collectgarbage() end
end
print("pairs", count(w), seen >= 10)

-- # of a weak array with holes
local a = setmetatable({}, {__mode = "v"})
for i = 1, 8 do a[i] = (i <= 4) and keep or {} end
collectgarbage()
print("#", #a, a[4] == keep, a[5])

-- setting a collected key again, and the table keeps working
local r = setmetatable({}, {__mode = "k"})
for i = 1, 100 do r[{}] = i end
collectgarbage()
for i = 1, 100 do r[i] = i end
local t = {}
r[t] = "t"
print("reuse", count(r), r[50], r[t])

-- a weak metatable entry: __index function only referenced by a weak table
local proto = setmetatable({}, {__mode = "v"})
proto.__index = function() return "idx" end
local obj = setmetatable({}, proto)
collectgarbage()
print("weak mt", proto.__index, obj.foo)
