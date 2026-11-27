-- tonumber(s) and tonumber(s, 10) of LuaJIT 2.1: lj_strscan.c with
-- STRSCAN_OPT_TONUM (white space, a sign, inf and nan, no FFI suffixes),
-- compared with luajit's output: the value as a hexadecimal float, or nil.
local function hex(v)
  if v == nil then return "nil" end
  if v ~= v then return "nan" end
  return string.format("%a", v)
end
local function esc(s)
  if #s > 60 then s = s:sub(1, 30) .. "...(" .. #s .. ")..." .. s:sub(-20) end
  return (string.gsub(s, "[^%w%p ]", function(c) return "\\" .. string.byte(c) end))
end
local cases = {
  -- white space and signs
  "1", " 1", "1 ", " 1 ", "\t\n\v\f\r1\r\n\f\v\t", "1 2", "- 1", "-1", "+1", "--1", "+-1", "-+1",
  "-0", "+0", "-0.0", "-0x10", "+0x10", "-0b11", "+0b11", "+.5", "-.5e1", "-", "+", "-.", ".",
  " -inf ", "\t+inf\n",
  -- inf and nan
  "inf", "INF", "Inf", "iNf", "-inf", "+inf", "infinity", "INFINITY", "-Infinity", "infinit",
  "infinityy", "infx", "inf x", "inf inity", "nan", "NaN", "NAN", "-nan", "+nan", "nan(1)",
  "nan()", "nan ", " nan", "nanx", "nana", "in", "i", "n", "na", "infnan", "nan inf", "-nan(1)",
  "x", "_1", "[1", "~1", "@1", "\2261", "\1951",
  -- empty strings and NUL bytes
  "", " ", "\0", "1\0", "\0" .. "1", "1 \0", "inf\0", "nan\0", "0x10\0", " \0", "1\0 ",
  -- decimal
  "10", "010", "00011", "1.", ".5", "5.", "-1.", "1e2", "1E+2", "1e-06", "+.5e1", "1e", "1e+",
  "e1", "1.5e3x", "1e400", "-1e400", "1e-400", "-1e-400", "1e1.5", "3.4.5", "1_000", "0o17",
  "123456789012345678901234567890", "9007199254740993", "4.9406564584124654e-324",
  "2.4703282292062328e-324", "2.4703282292062327e-324", "1e1048575", "1e1048576", "1e-1048575",
  "1e00000000000000000005", "1e5 ", " 1e5", "1 e5", "1e 5", "1e- 5",
  -- hexadecimal and binary
  "0x", "0x10", "0X1f", " 0x10 ", "0x-1", "0x1p4", "0x1.8", "0x.8", "0xA.8p1", "0x1p", "0x.",
  "0x1e+1", "0x1P-1074", "0x1p-1075", "0x1.8p-1075", "0xffffffffffffffffff", "0x1p1048576",
  "0x7fffffffffffffff", "0b", "0b101", "0B11", "0b2", "0b1.0", "0b" .. string.rep("1", 64),
  "0b" .. string.rep("1", 65), "0b" .. string.rep("1", 54), "0xg", "0x 1", "0 x1",
  -- the FFI's suffixes are not accepted
  "1LL", "1ULL", "1i", "1U", "1L", "0x10LL", "1.5i", "1 LL", "1LL ", "1 i",
}
for _, s in ipairs(cases) do
  print(esc(s), hex(tonumber(s)), hex(tonumber(s, 10)))
end
-- long strings
local h = "9007199254740993"
for _, z in ipairs({780, 791, 800, 801, 900}) do
  print(z, hex(tonumber(h .. string.rep("0", z) .. "e-" .. z)), hex(tonumber(" " .. h .. string.rep("0", z) .. "e-" .. (z + 1) .. " ")),
    hex(tonumber(h .. string.rep("0", z) .. "1e-" .. (z + 1))), hex(tonumber("-" .. h .. "." .. string.rep("0", z))))
end
print(hex(tonumber("0." .. string.rep("0", 1048574) .. "1")), hex(tonumber("0." .. string.rep("0", 1048575) .. "1")))
print(hex(tonumber(string.rep(" ", 100000) .. "1" .. string.rep(" ", 100000))))
-- numbers and other values
print(tonumber(10), tonumber(-0.5), tonumber(1/0), tonumber(nil), tonumber(true), tonumber({}))
