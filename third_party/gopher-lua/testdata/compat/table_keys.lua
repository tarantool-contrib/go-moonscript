-- go-moonscript: clearing the fields of a table during its traversal, and
-- tables whose keys are added and removed many times (yuin/gopher-lua#214).
local function count(t) local n = 0 for _ in pairs(t) do n = n + 1 end return n end
local t = {}
for i = 1, 20000 do t["k" .. i] = i end
for i = 1, 20000 do t[i + 0.5] = i end
local seen = 0
for k, v in pairs(t) do seen = seen + 1 t[k] = nil end
print(seen, count(t), next(t))
for i = 1, 100000 do t["x" .. i] = i if i > 5 then t["x" .. (i - 5)] = nil end end
print(count(t), t.x99996, t.x100000, t.x99995)
local sum = 0
for k, v in pairs(t) do sum = sum + v if v % 2 == 0 then t[k] = nil end end
print(sum, count(t))
local q, first, last = {}, 1, 0
for i = 1, 200000 do last = last + 1 q[last] = i if last - first >= 3 then q[first] = nil first = first + 1 end end
print(count(q), q[first], q[last], #q == 0 or #q == last)
