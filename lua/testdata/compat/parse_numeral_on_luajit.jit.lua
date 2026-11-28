-- LuaJIT 2.1's numerals (lj_lex.c's lex_number, lj_strscan.c with the FFI's
-- suffixes), compared with luajit's output: the value of each numeral as a
-- hexadecimal float, or its error. The FFI's 64-bit integers (1LL, 1ULL)
-- and imaginary numbers (1i) are cdata: luajit evaluates them, the VM
-- accepts them and raises an error when they are evaluated; both print
-- "cdata".
local function hex(v)
  if v ~= v then return "nan" end
  return string.format("%a", v)
end
local function cut(s)
  if #s > 120 then s = s:sub(1, 60) .. "...(" .. #s .. ")..." .. s:sub(-40) end
  return s
end
local function show(s)
  local f, err = loadstring("return " .. s, "=n")
  if not f then return cut((string.gsub(err, "^.-:(%d+): ", "%1: "))) end
  local ok, v = pcall(f)
  if ok and type(v) == "cdata" or not ok and string.find(v, "needs the FFI", 1, true) then
    return "cdata"
  end
  if not ok then return "error " .. v end
  return hex(v)
end
local function esc(s)
  s = cut(s)
  return (string.gsub(s, "[^%w%p ]", function(c) return "\\" .. string.byte(c) end))
end
local cases = {
  -- decimal
  "0", "00", "1", "007", "1.", ".5", "1.5", "0.1", "1e5", "1E+5", "1e-5", "1.e5", "1.5e-3",
  "123456789012345678901234567890", "9007199254740993", "9007199254740995",
  "1e308", "1.7976931348623157e308", "1.7976931348623158e308", "1.7976931348623159e308",
  "1e309", "1e400", "4.9406564584124654e-324", "2.4703282292062328e-324",
  "2.4703282292062327e-324", "1e-400", "2.2250738585072011e-308", "2.2250738585072012e-308",
  "1e", "1e+", "1e-", "1.5e", "1..5", "3.4.5", "1e1.5", "1e5.", "1x", "1a", "1f", "1_000",
  "1end", "1e+5+3", "1-1", "1e-1-1", "1E-2",
  -- exponent limits
  "1e1048575", "1e1048576", "1e-1048575", "0e1048575", "0e1048576", "1e00000000000000000005",
  -- hexadecimal
  "0x", "0X", "0x.", "0x.8", "0x1.", "0x1p", "0x1p+", "0x1p-", "0x1p4", "0X1P-4", "0xA.8p1",
  "0x1e+1", "0xe+1", "0xep1", "0xg", "0x1g", "0x1.8", "0x.p1", "0x1p1023", "0x1p1024",
  "0x1.fffffffffffffp1023", "0x1.fffffffffffff8p1023", "0x1p-1022", "0x1p-1074", "0x1p-1075",
  "0x1.0000000000001p-1075", "0x1.8p-1074", "0x1.8p-1075", "0x3p-1076", "0x1.00000000000008p0",
  "0x1.000000000000080000000001p0", "0x1.00000000000018p0", "0x1.000000000000180p0",
  "0xffffffffffffffff", "0xffffffffffffffffff", "0x123456789abcdef0123", "0x00000000000000000001",
  "0x1P", "0x1p1048575", "0x1p1048576", "0x10e", "0x1.p1", "0x1..2",
  -- binary
  "0b", "0B", "0b0", "0b1", "0b101", "0B11", "0b2", "0b12", "0b1.0", "0b.1", "0b1.", "0b11e1",
  "0b1p1", "0b" .. string.rep("1", 53), "0b" .. string.rep("1", 54), "0b" .. string.rep("1", 64),
  "0b" .. string.rep("1", 65), "0b" .. string.rep("0", 70) .. "1", "0b1" .. string.rep("0", 63),
  -- the FFI's suffixes
  "1LL", "1ll", "1lL", "1uLL", "1ULL", "1ull", "1LLU", "1llu", "1LLu", "1U", "1u", "1L", "1l",
  "1UL", "1LU", "1LLL", "1ULLU", "1LLUU", "1UU", "1i", "1I", "1.5i", ".5i", "1e3i", "1e-3I",
  "0x10i", "0x1p4i", "0b11i", "1ii", "1iLL", "1LLi", "1.5LL", "1e2LL", "1.LL", "0x1p4LL",
  "0x10LL", "0x10ULL", "0xffffffffffffffffULL", "0xffffffffffffffffLL",
  "0x10000000000000000LL", "0x00000000000000000001LL", "18446744073709551615ULL",
  "18446744073709551616ULL", "18446744073709551615LL", "9223372036854775808LL",
  "99999999999999999999LL", "100000000000000000000LL", "00000000000000000000000001LL", "0LL",
  "0ll", "0ULL", "00LL", "0b1LL", "0b" .. string.rep("1", 64) .. "ULL",
  "0b" .. string.rep("1", 65) .. "ULL", "0b2LL", "0xLL", "0bLL", "1LLx", "1i5", "1e5LL",
}
for _, s in ipairs(cases) do
  print(esc(s), show(s))
end
-- long numerals: digits after the 800th, fractional digits up to 2^20
local h = "9007199254740993"
for _, z in ipairs({780, 791, 800, 801, 900}) do
  print(z, show(h .. string.rep("0", z) .. "e-" .. z), show(h .. string.rep("0", z) .. "e-" .. (z + 1)),
    show(h .. string.rep("0", z) .. "1e-" .. (z + 1)), show(h .. "." .. string.rep("0", z)))
end
print(show("1" .. string.rep("0", 400)), show("0." .. string.rep("0", 330) .. "1"))
print(show("0." .. string.rep("0", 1048574) .. "1"), show("0." .. string.rep("0", 1048575) .. "1"))
print(show("1." .. string.rep("0", 1048580)), show(".1" .. string.rep("0", 1048580)))
print(show(string.rep("1", 1048580) .. ".5e-1048400"), show("0x1." .. string.rep("0", 1048574) .. "1p4"),
  show("0x1." .. string.rep("0", 1048574) .. "1p1048575"))
print(show("1" .. string.rep("0", 1048576) .. "LL"), show("0x" .. string.rep("0", 1048576) .. "1LL"))
-- operators after a numeral
print(1e+5+3, 0x1p-1-1, 0xe+1, 1-1, 2^-1, 0x10 .. "", 1 .. 2)
