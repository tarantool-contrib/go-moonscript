-- yuin/gopher-lua#478, halogencapital/gopher-lua d3de08565a: strings convert
-- to numbers as with strtod (tonumber and arithmetic)
local function fmt(v)
  if type(v) ~= "number" then return tostring(v) end
  if v ~= v then return "nan" end
  if v == 1/0 then return "inf" end
  if v == -1/0 then return "-inf" end
  if v == 0 and 1/v < 0 then return "-0" end
  return string.format("%.14g", v)
end
local cases = {"10", " 10 ", "010", "00011", "0x10", "0X1f", " 0x10 ", "-0x10",
  "+5", "-5", "1e2", "1E+2", "1e-06", "+.5e1", ".5", "5.", "-1.", ".", "0x", "1e",
  "1e+", "e1", "inf", "-inf", "INF", "nan", "-nan", "infinity", "infin", "nan(123)",
  "0x1p4", "0x1.8", "0x.8", "0xA.8p1", "0x1p", "1_000", "0b101", "0o17", "1.5e3x",
  "", " ", "\t5\n", "\v5\f\r", "5\0", "5\0x", "0x7fffffffffffffff",
  "0xffffffffffffffffff", "1e400", "-1e400", "1e-400", "-0", "0x-1", "- 1", "1 2",
  "1e1.5", "3.4.5", "0x1e+1"}
for _, s in ipairs(cases) do
  local ok, a = pcall(function() return s + 0 end)
  local ok2, b = pcall(function() return s * "1" end)
  print("[" .. string.gsub(s, "%c", function(c) return "\\" .. string.byte(c) end) .. "]", fmt(tonumber(s)), ok and fmt(a) or "error", ok2 and fmt(b) or "error", fmt(tonumber(s, 10)))
end
print("00001" + "00011", "010" * 1, -"0x10", 10 .. "" == "10")
print(math.floor("3.7"), math.max("0x10", "1e1"))
