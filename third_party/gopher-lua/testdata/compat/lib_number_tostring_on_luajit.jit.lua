-- The cases of lib_number_tostring.lua under DialectLuaJIT, compared with luajit's output.
-- numbers convert to strings with Lua 5.1's "%.14g" (lua_number2str)
local nums = {0, 1, -1, 100, 0.5, 0.1, 0.1 + 0.2, 1/3, -2/3, 100/3, 1e-5, 123e-7,
  99999999999999, 1e14, 123456789012345, 1e15, 2^53, 2^63, -2^63, 2^64, 1e100,
  1e300 * 10, 5e-324, 2^-1074, 1/0, -1/0, 0/0, -(0/0)}
for i, n in ipairs(nums) do
  print(i, tostring(n), n .. "", "" .. n, table.concat({n, n}, ","))
end
-- concatenation and string coercion of the results
print(1e15 .. 2^53, (1/3) .. "|" .. (2/3))
print(#(1/3 .. ""), ("x"):rep(2) .. 1e20)
print(string.len(1/3), string.upper(1e15), string.sub(2^53, 1, 5))
-- math.huge is HUGE_VAL, the positive infinity
print(math.huge, -math.huge, math.huge == 1/0, -math.huge == -1/0, math.huge > 1.7976931348623157e308)
-- negative zero keeps its sign, also once stored as a value
local z = 0
z = -z
print(z, z .. "", 1/z, tostring(z), table.concat({z}))
local a = 0
a = a * -1
print(a, 1/a)
local t = {}
t[1] = a
print(t[1], 1/t[1])
print(math.floor(-0.5), math.ceil(-0.5))
