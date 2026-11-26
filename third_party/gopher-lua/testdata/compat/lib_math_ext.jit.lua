-- LuaJIT 2.1: math.log(x [, base]) = log2(x) * (1 / log2(base)); math.max
-- and math.min as maxsd/minsd (NaNs and signed zeros); no math.mod
local function g(x) return string.format("%.17g", x) end
print(math.mod, type(math.log10), g(math.log(8)), g(math.log(100)))
for _, b in ipairs({2, 10, 3, 5, 7, 0.5, 16, 1e10, 1}) do
  local r = {}
  for _, x in ipairs({1, 2, 3, 8, 10, 100, 1000, 1e15, 0.1, 0.001, 7, 1/3, 2^40, 3^20, 12345.678, 1e-300, 5e-324, 1.7976931348623157e308}) do
    r[#r + 1] = g(math.log(x, b))
  end
  print(b, table.concat(r, " "))
end
print(g(math.log(-8, 2)), g(math.log(0, 2)), g(math.log(8, -2)), g(math.log(1/0, 2)), g(math.log(8, 1/0)), g(math.log(8, 0)), g(math.log(0/0, 2)))
print(math.log("100", "10"), (select(2, pcall(math.log, 8, nil)):match("number expected, got nil")), (select(2, pcall(math.log, 8, {})):match("number expected, got table")))
-- many values (a check of log2's accuracy)
local x, s = 1.2345, {}
for i = 1, 400 do
  x = x * 1.6180339887 + 0.0001
  if x > 1e300 then x = x / 1e299 end
  s[#s + 1] = g(math.log(x, 2)) .. " " .. g(math.log(x, 10)) .. " " .. g(math.log(i, 3))
end
for i = 1, #s, 40 do print(table.concat(s, " ", i, math.min(i + 39, #s))) end
local z = 0
local nz = -z
print(math.max(0/0, 1, 2), math.max(1, 0/0, 2), math.max(1, 2, 0/0), math.min(0/0, 2, 1), math.min(2, 0/0, 1), math.min(2, 1, 0/0))
print(1/math.max(nz, 0), 1/math.max(0, nz), 1/math.min(0, nz), 1/math.min(nz, 0), math.max(3), math.min("4", 2), math.max(1, "5"))
print((select(2, pcall(math.max)):match("number expected")), (select(2, pcall(math.min, 1, nil)):match("number expected, got nil")))
