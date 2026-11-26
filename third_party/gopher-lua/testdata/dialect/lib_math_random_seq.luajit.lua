-- math.random and math.randomseed under DialectLuaJIT: LuaJIT 2.1's generator
-- (output recorded from luajit 2.1 in ref/, see lib_fix_dialect_test.go)
local function seq(...)
  local t = {}
  for i = 1, 4 do t[#t + 1] = string.format("%.17g", math.random(...)) end
  return table.concat(t, " ")
end
print(seq(), seq(10), seq(-5, 5), seq(3, 1), seq(2.5), seq(1, 2, 3))
for _, s in ipairs({0, 1, 42, -1, 2^31, 1e20, 3.9, 0.5, "17"}) do
  math.randomseed(s)
  print(s, seq(), seq(100), seq(-2^40, 2^40))
end
math.randomseed(7)
local sum = 0
for _ = 1, 100000 do sum = sum + math.random(1000) end
print(sum, math.mod)
