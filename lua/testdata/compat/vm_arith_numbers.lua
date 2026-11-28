-- go-moonscript patch: the number cases of the VM's arithmetic, comparison
-- and numeric for opcodes and of the boxing of their results (alloc.go):
-- -0, nan, inf, the preloaded small integers and their limits, constant
-- operands, string coercion and the error messages.
local function show(...)
  local t = {}
  for i = 1, select("#", ...) do
    local v = select(i, ...)
    if v ~= v then
      t[#t + 1] = "nan" -- its sign and its tostring depend on the platform
    elseif type(v) == "number" then
      t[#t + 1] = tostring(v) .. "|" .. string.format("%.17g", v) .. "|" .. tostring(1 / v)
    else
      t[#t + 1] = tostring(v)
    end
  end
  print(table.concat(t, " "))
end

local zero, one, two, half = 0, 1, 2, 0.5
local nz = -zero
show(nz, -0, 0 * -1, zero * -one, -(zero), nz + 0, nz - 0, nz * 1, nz / 1)
show(zero - zero, nz - nz, nz + nz, 0 / -1, -zero / one)
show(math.ceil(-0.5), math.floor(-0), math.abs(nz), math.max(nz), math.min(nz), math.fmod(-1, 1))
show(math.sqrt(nz), nz ^ 1, nz % 1, -1 % 1, 1 % -1, 5 % math.huge, -5 % math.huge)
local nan = zero / zero
show(nan ~= nan, nan == nan, nan < nan, nan <= nan, nan > 1, 1 < nan, -nan ~= -nan)
show(1 / zero, -1 / zero, (1 / zero) - (1 / zero), (1 / zero) * zero, math.huge % 2)
for _, v in ipairs({ 126, 127, 127.5, 128, 129, -1, -0.5, 0.5, 2 ^ 53, 2 ^ 63, -2 ^ 63, 1e300 * 1e10 }) do
  show(v, v + 0, v - 0, v * 1, v / 1, -v, v + 1, v - 1)
end
show(3 % 0.1, 5.5 % 2, -5.5 % 2, 5.5 % -2, 7 % 3, -7 % 3, 7 % -3, 2 ^ 0.5, (-8) ^ (1 / 3), 2 ^ -1074)
show(one + 2, 2 + one, one - 2, 2 - one, two * 3, 3 * two, one / 4, 4 / two, 7 % two, 2 ^ two, two ^ 10)
show(1 + 2, "10" + 1, 1 + "0x10", "3" * "4", "2" ^ "3", -"2", "  5  " - 1, "1e1" / 2, "7" % "4")
show(1 < 2, 2 < 1, 1 <= 1, 2 <= 1, 1 == 1, 1 == "1", one == 1, zero == nz, nz < zero, "a" < "b", "b" <= "a")
local acc = 0
for i = 1, 10 do acc = acc + i end
for i = 10, 1, -3 do acc = acc + i * 100 end
for i = 0, 1, 0.25 do acc = acc + i end
for i = 1, 0 do acc = acc + 1000000 end
for i = 1.5, 3.5 do acc = acc + i * 0.001 end
for i = -0.0, 0 do show(i) end
for i = 120, 135, 5 do show(i) end
for i = "1", "3", "1" do acc = acc + i * 10000 end
show(acc)
local t = {}
for i = 1, 3 do t[i] = function() return i end end
show(t[1](), t[2](), t[3]())

local function err(f, ...)
  local ok, msg = pcall(f, ...)
  print(ok, msg)
end
local x
local tab = { field = nil }
err(function() return x + 1 end)
err(function() return 1 - x end)
err(function() return tab.field * 2 end)
err(function() return tab.missing / 2 end)
err(function() return x % 2 end)
err(function() return 2 ^ x end)
err(function() return -x end)
err(function() return -tab end)
err(function() return "a" + 1 end)
err(function() return {} + 1 end)
err(function() return glob + 1 end)
err(function() return x < 1 end)
err(function() return 1 <= x end)
err(function() return {} < {} end)
err(function() return "1" < 2 end)
err(function() for i = x, 2 do end end)
err(function() for i = 1, x do end end)
err(function() for i = 1, 2, x do end end)
err(function() for i = 1, 2, "a" do end end)
local mt = { __add = function(a, b) return "add" end, __unm = function() return "unm" end,
  __lt = function() return true end, __le = function() return false end,
  __eq = function() return true end, __mod = function() return "mod" end, __pow = function() return "pow" end }
local a, b = setmetatable({}, mt), setmetatable({}, mt)
show(a + 1, 1 + a, -a, a < b, a <= b, a == b, a ~= b, a % 2, 2 ^ a)
