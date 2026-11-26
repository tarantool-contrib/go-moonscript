-- The cases of lib_tonumber_base.lua under DialectLuaJIT, compared with luajit's output.
-- tonumber(s, base) for bases other than 10 as Lua 5.1's (C's strtoul)
-- (a '-': lib_tonumber_base_neg.jit.lua)
local cases = {"10", "010", " 10 ", "\t10\n", "10\v", "\f10\r", "0x10", "0X1f",
  "+0x10", "0x", "0x-1", "0xg", "1e2", "ff", "FF", "+ff", "- ff", "zz", "ZZ", "z1",
  "11", "12", "2", "1.5", "1 2", "10a", "", " ", "-", "+", "--1", "+-1", "10\0", "1\0 2",
  "ffffffffffffffff", "10000000000000000", "1ffffffffffffffff",
  "zzzzzzzzzzzzzzz", "11111111111111111111111111111111111111111111111111111111111111111",
  "1111111111111111111111111111111111111111111111111111111111111111", "777"}
for i, s in ipairs(cases) do
  print(i, tonumber(s, 2), tonumber(s, 8), tonumber(s, 16), tonumber(s, 36), tonumber(s, 10))
end
-- a number is converted to a string first
print(tonumber(10, 16), tonumber(1e15, 16), tonumber(255, 36), tonumber(0.5, 16))
print(tonumber("10", 10.9), tonumber("z", 36.5))
-- (the messages are "bad argument #2 to 'tonumber' (base out of range)" and
-- "bad argument #1 to 'tonumber' (string expected, got ...)")
print((pcall(tonumber, "10", 1)), (pcall(tonumber, "10", 37)), (pcall(tonumber, "10", 0)),
  (pcall(tonumber, "10", -16)), (pcall(tonumber, "10", 36)), (pcall(tonumber, "10", 2)))
print((pcall(tonumber, nil, 16)), (pcall(tonumber, {}, 16)), (pcall(tonumber, true, 2)))
print(tonumber(nil), tonumber(true), tonumber({}), tonumber("0x10"), tonumber("10", nil))
