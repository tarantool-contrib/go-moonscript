-- math.random and math.randomseed give C rand()'s sequences (glibc), the
-- default one (seed 1) included
local function seq(...)
  local t = {}
  for i = 1, 5 do t[#t + 1] = string.format("%.17g", math.random(...)) end
  return table.concat(t, " ")
end
print("default", seq())
print("default m", seq(10))
print("default m n", seq(-5, 5))
for _, s in ipairs({0, 1, 2, 42, -1, -123456, 2^31 - 1, 2^31, 2^32 + 7, 1e20, -1e20, 3.9, -3.9, "17"}) do
  math.randomseed(s)
  print(s, seq())
  math.randomseed(s)
  print(s, seq(100), seq(1, 6), seq(-2^40, 2^40), seq(2^31 - 2, 2^31 - 1))
end
math.randomseed(0 / 0)
print("nan", seq())
-- a long run: the feedback table wraps many times
math.randomseed(7)
local sum = 0
for _ = 1, 100000 do sum = sum + math.random(1000) end
print("sum", sum, math.random())
-- arguments that luaL_checkint truncates
math.randomseed(5)
print(seq(3.99), seq(1.5, 3.5), seq("4"))
