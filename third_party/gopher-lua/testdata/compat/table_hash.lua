-- The hash part of tables (go-moonscript patch: table_hash.go): keys
-- of every type, deletes, re-adds, growth, and next/pairs, printed in an
-- order that doesn't depend on the iteration order.
local function sorted(t)
  local ks = {}
  for k, v in pairs(t) do ks[#ks + 1] = type(k) .. ":" .. tostring(k) .. "=" .. tostring(v) end
  table.sort(ks)
  return table.concat(ks, " ")
end

local f, g = function() end, function() end
local tk = {}
local t = {x = 1, y = 2, [1] = "a", [2] = "b", [2.5] = "f", [-1] = "n", [true] = "T", [false] = "F"}
t[f] = "fn"; t[tk] = "tbl"; t[2^40] = "big"; t[-0.5] = "neg"
print(sorted(t):gsub("0x%x+", "ADDR"))
print(t.x, t.y, t[1], t[2], t[2.5], t[-1], t[true], t[false], t[f], t[g], t[tk], t[2^40])

-- -0 and 0 are the same key
local z = {}
z[0] = "zero"
print(z[-0], z[0])
z[-0] = "minus zero"
print(z[0])
local n = 0
for k in pairs(z) do n = n + 1 end
print("keys", n)

-- deleting and adding back
local d = {}
for i = 1, 100 do d["k" .. i] = i end
for i = 1, 100, 2 do d["k" .. i] = nil end
local cnt, sum = 0, 0
for k, v in pairs(d) do cnt = cnt + 1; sum = sum + v end
print(cnt, sum, d.k1, d.k2, d.k99, d.k100)
for i = 1, 100, 4 do d["k" .. i] = -i end
cnt, sum = 0, 0
for k, v in pairs(d) do cnt = cnt + 1; sum = sum + v end
print(cnt, sum, d.k1, d.k3, d.k5)

-- clearing every field during the traversal
local c = {}
for i = 1, 50 do c["f" .. i] = i; c[i + 0.5] = i end
local seen = 0
for k in pairs(c) do c[k] = nil; seen = seen + 1 end
print(seen, next(c))

-- assigning existing fields during the traversal
local a = {p = 1, q = 2, r = 3}
for k, v in pairs(a) do a[k] = v * 10 end
print(sorted(a))

-- next by hand
local h = {u = 1}
print(next(h))
print(next(h, "u"))
print(next({}))

-- growth: many keys of mixed types
local big = {}
for i = 1, 3000 do big["s" .. i] = i; big[-i] = i; big[i + 0.25] = i end
cnt, sum = 0, 0
for k, v in pairs(big) do cnt = cnt + 1; sum = sum + v end
print(cnt, sum, big.s1234, big[-77], big[10.25])
for i = 1, 3000 do big["s" .. i] = nil end
cnt = 0
for k, v in pairs(big) do cnt = cnt + 1 end
print(cnt, big.s1)

-- constructors with many hash fields, globals
local cons = {a = 1, b = 2, c = 3, d = 4, e = 5, f = 6, g = 7, h = 8, i = 9, j = 10, k = 11}
print(sorted(cons))
gx, gy = 1, 2
gx = nil
print(gx, gy, rawget(_G, "gy"))

-- the array part and the hash part together
local m = {10, 20, 30, n = "x"}
m[4] = 40
m[6] = 60
m.n = nil
print(#m >= 4, m[4], m[5], m[6], sorted(m))
