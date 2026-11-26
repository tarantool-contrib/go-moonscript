-- table.sort sorts t[1..#t] with Lua 5.1's quicksort
local function show(t, n) local o = {} for i = 1, n or #t do o[i] = tostring(t[i]) end return table.concat(o, " ") end
local t = {3, 1, 2}; table.sort(t); print(show(t))
local t = {}; t[3] = 1; t[2] = 3; t[1] = 2; table.sort(t); print(show(t), #t)
local t = {3, 1, 2, nil}; table.sort(t); print(show(t, 4), #t)
local t = {5, 4, 3, 2, 1}; t[5] = nil; table.sort(t); print(show(t, 5), #t)
local t = {}; for i = 1, 10 do t[i] = 11 - i end; for i = 6, 10 do t[i] = nil end
table.sort(t); print(show(t, 10))
local t = {5, 4, 3, 2, 1}; t[3] = nil; print(#t, (pcall(table.sort, t)))
print((pcall(table.sort, {1, "x"})))
local t = {3, 2, 1}; table.sort(t, nil); print(show(t))
local t = {3, 2, 1}; table.sort(t, function(a, b) return a > b end); print(show(t))
-- the order of equal elements is Lua 5.1's
local recs = {}
for i = 1, 40 do recs[i] = {k = (i * 7) % 5, id = i} end
table.sort(recs, function(a, b) return a.k < b.k end)
local o = {}
for i, r in ipairs(recs) do o[i] = r.k .. ":" .. r.id end
print(table.concat(o, " "))
local words = {"pear", "fig", "apple", "kiwi", "plum", "date", "lime", "nut", "yam", "pea"}
table.sort(words, function(a, b) return #a < #b end)
print(table.concat(words, " "))
local t = {}
for i = 1, 200 do t[i] = (i * 37) % 101 end
table.sort(t)
local ok = true
for i = 2, 200 do ok = ok and t[i - 1] <= t[i] end
print(ok, t[1], t[200])
local mt = {__lt = function(a, b) return a.v < b.v end}
local t = {setmetatable({v = 3}, mt), setmetatable({v = 1}, mt), setmetatable({v = 2}, mt)}
table.sort(t)
print(t[1].v, t[2].v, t[3].v)
-- an invalid order function
local ok, err = pcall(function() table.sort({5, 3, 4, 1, 2, 9, 8, 7, 6}, function() return true end) end)
print(ok, err)
local t = {5, 3, 4, 1, 2, 9, 8, 7, 6}
print(pcall(table.sort, t, function(a, b) return a <= b end))
print(show(t))
-- an error in the comparator leaves the table partly sorted
local t = {9, 8, 7, 6, 5, 4, 3, 2, 1}
local calls = 0
print((pcall(table.sort, t, function(a, b) calls = calls + 1 if calls == 5 then error("stop") end return a < b end)))
print(show(t))
print((pcall(table.sort, {3, 2, 1}, 42)), (pcall(table.sort)))
