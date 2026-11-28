-- go-moonscript: the iteration order of tables whose keys are strings,
-- numbers and booleans is Lua 5.1's (table_hash.go has its nodes).
local function show(t)
  local r = {}
  for k, v in pairs(t) do r[#r + 1] = tostring(k) .. "=" .. tostring(v) end
  print(table.concat(r, " "))
end
show({a = 1, b = 2, c = 3, d = 4, e = 5})
show({x = 1, y = 2, [1] = 1, [2] = 2, [10] = 3, [2.5] = 4, [true] = 5, [false] = 6, name = "n", value = "v"})
local t = {} for i = 1, 20 do t["k" .. i] = i end show(t)
t = {} for i = 1, 20 do t[i * 3] = i end show(t)
t = {} for i = 1, 30 do t["k" .. i] = i end
for i = 1, 30, 3 do t["k" .. i] = nil end
for i = 31, 40 do t["k" .. i] = i end show(t)
t = {10, 20, 30, n = 3, [-1] = 0, [0] = 0, [1e10] = 1, [0.1] = 2} show(t)
t = {} for i = 1, 50 do t[("x"):rep(i % 7 + 1) .. i] = i end show(t)
t = {"a", "b", "c", "d"} t[2] = nil t[6] = "f" t.z = "z" t[5] = "e" show(t)
local s = {}
for k, v in pairs({one = 1, two = 2, three = 3, four = 4}) do s[#s + 1] = k end
print(table.concat(s, " "), next({}), next({p = 1}))
