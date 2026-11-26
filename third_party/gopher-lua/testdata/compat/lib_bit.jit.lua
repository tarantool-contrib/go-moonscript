-- LuaJIT 2.1: the bit library (lib_bit.c), 32-bit operations on doubles:
-- arguments converted as lj_num2bit (round to nearest even, modulo 2^32),
-- signed 32-bit results; bit.tohex as in the FFI build
print(type(bit), package.loaded.bit == bit, require("bit") == bit)
local names = {}
for k in pairs(bit) do names[#names + 1] = k end
table.sort(names)
print(table.concat(names, " "))
local vals = {0, 1, -1, 1.5, 2.5, -2.5, -0.5, 0.49999999999999994, 2^31, 2^31 - 1, -2^31, -2^31 - 1,
  2^32, 2^32 + 7, -2^32 - 7, 2^52, 2^52 + 0.5, 2^53 + 2, 2^63, -2^63, 1e100, -1e100, 1/0, -1/0, 0/0,
  0x12345678, 0xdeadbeef, 0x7fffffff, 0x80000000, 0xffffffff, "10", "0x10", " 12 ", "1e3"}
for _, v in ipairs(vals) do
  print(v, bit.tobit(v), bit.bnot(v), bit.bswap(v), bit.tohex(v), bit.tohex(v, -4), bit.band(v, 0xff00ff), bit.bor(v, 1), bit.bxor(v, -1))
end
for _, n in ipairs({0, 1, 4, 31, 32, 33, -1, -31, 63, 2^32 + 3, 1.5, 0/0}) do
  local x = 0x87654321
  print(n, bit.lshift(x, n), bit.rshift(x, n), bit.arshift(x, n), bit.rol(x, n), bit.ror(x, n),
    bit.lshift(1, n), bit.arshift(-256, n))
end
for _, n in ipairs({0, 1, 2, 7, 8, 9, 15, 16, 17, 20, -1, -8, -9, -16, -20, 254, 255, 256, 2^32 + 3, -2^31}) do
  print(n, "[" .. bit.tohex(0x12345678, n) .. "]", "[" .. bit.tohex(-1, n) .. "]", "[" .. bit.tohex(0, n) .. "]")
end
print(bit.band(7), bit.band(7, 3, 5), bit.bor(1, 2, 4, 8, 16), bit.bxor(5, 3, 1, 7), bit.band(-1, -1, -1, 0x7fffffff))
print(bit.tobit(1, 2), bit.lshift(1, 2, 3), bit.tohex(255, 4, 9))
print(1 / bit.tobit(-0), bit.tobit(-0) == 0)
for _, f in ipairs({"tobit", "band", "lshift", "tohex"}) do
  print(f, pcall(bit[f]), (select(2, pcall(bit[f], "x")):match("number expected, got string")),
    (select(2, pcall(bit[f], {})):match("number expected, got table")))
end
print((pcall(bit.lshift, 1)), (pcall(bit.band, 1, nil)))
-- the usual idioms
local function crc32(s)
  local crc = -1
  for i = 1, #s do
    crc = bit.bxor(crc, s:byte(i))
    for _ = 1, 8 do
      crc = bit.bxor(bit.rshift(crc, 1), bit.band(0xedb88320, -bit.band(crc, 1)))
    end
  end
  return bit.bnot(crc)
end
print(bit.tohex(crc32("The quick brown fox jumps over the lazy dog")))
