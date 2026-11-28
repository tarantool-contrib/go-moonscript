-- os.date as Lua 5.1's: every "%<char>" is C's strftime in the C locale
-- (UTC here, "!", so that the output does not depend on the time zone)
local function q(s)
  return '"' .. string.gsub(s, '[%c"\\\128-\255]', function(c) return "\\" .. string.byte(c) end) .. '"'
end
local times = {1136214245, 0, 1700000000, 951829200, -946080000, 1230681600, 1104537600,
  1293753600, 1609459199, 2^31, 86399.9}
for c = 32, 126 do
  local ch = string.char(c)
  local out = {}
  for _, t in ipairs(times) do out[#out + 1] = q(os.date("!%" .. ch, t)) end
  print(ch, table.concat(out, " "))
end
print(os.date("!%Y-%m-%d %H:%M:%S|%x|%X|%c|%%|%", 1136214245))
print(os.date("!%-d|%_d|%Ec|%5|%10Y|plain text%", 1136214245))
print(q(os.date("!%\1x%\200y", 0)))
-- "*t" (exactly), with yday and isdst
for _, t in ipairs(times) do
  local d = os.date("!*t", t)
  print(d.year, d.month, d.day, d.hour, d.min, d.sec, d.wday, d.yday, d.isdst)
end
print(os.date("!*tx", 0), os.date("!*T", 0))
local d = os.date("*t")
print(type(d.yday), d.yday >= 1 and d.yday <= 366, type(d.isdst))
-- optional arguments: nil is the default
print(type(os.date()), type(os.date(nil)), type(os.date(nil, nil)), os.date(2, 0) == "2")
print(os.date("!%Y", nil) == os.date("!%Y"), os.date("!%x", nil) == os.date("!%x"))
print(os.date("!%H:%M", "3600"), os.date("!%S", 1.9), os.date("!%Y-%m-%d", -86400))
print((pcall(os.date, {})), (pcall(os.date, "%Y", "x")), (pcall(os.date, "%Y", {})))
-- a time out of the range of struct tm
print(os.date("!%Y", 2^60), os.date("!*t", -2^60))
