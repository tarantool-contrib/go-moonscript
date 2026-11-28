-- yuin/gopher-lua#514, #528: a generic for with fewer than three expressions
-- sets the state and control to nil
local tbl = {foo = 42, bar = "baz"}
local iter = function(s, k)
  k, _ = next(tbl, k)
  return k
end
local seen1, seen2 = {}, {}
for item in iter do seen1[item] = true end
for item in iter do seen2[item] = true end
print(seen1.foo, seen1.bar, seen2.foo, seen2.bar)
local function show(s, c)
  print("iter", s, c)
  if c == nil then return 1 end
end
local junk1, junk2, junk3 = "a", "b", "c"
junk1, junk2, junk3 = nil, nil, nil
do local x, y, z = 7, 8, 9 end
for v in show do print(v) end
do local x, y, z = 7, 8, 9 end
for v in show, "s" do print(v) end
do local x, y, z = 7, 8, 9 end
for a, b, c, d in show, "s", nil, 99 do print(a, b, c, d) end
local function g() return 1, 2, 3, 4 end
for a, b, c, d in g do print(a, b, c, d) break end
