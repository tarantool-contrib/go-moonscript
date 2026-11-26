-- numerals are scanned like Lua 5.1's read_numeral (malformed numbers)
local function fmt(v)
  if v ~= v then return "nan" end
  return string.format("%.14g", v)
end
for _, src in ipairs{"1e", "1e+", "1E-", "0x", "3..2", "1x", "0x1p4", "0x1P-1",
    "08", "010", "1.5.5", "0xg", "1e5", "0x1F", "3 .. 2", "0x1e+1", ".5", "5.",
    "1e+5", "1.e2", ".e2", "0x.1", "1_0", "0b1", "1.5f", "2e2e2"} do
  local f, err = loadstring("return " .. src)
  print(src, f and fmt(tonumber(f())) or (string.find(err, "malformed number", 1, true) and "malformed") or "error")
end
