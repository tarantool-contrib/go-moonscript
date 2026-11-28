-- LuaJIT 2.1 converts numbers to strings with "%.14g", exact ties rounded
-- away from zero, and "nan" for every NaN (tostring, print, .., string
-- functions)
local z = 0
local nz = -z
local nan = 0/0
print(tostring(nan), tostring(-nan), nan, -nan, nan .. "", -nan .. "|", ("x"):rep(1) .. -nan)
print(123456789012345, 12345678901234.5, -123456789012345, 0.5, 1.00000000000005, 2^63, -2^63)
print(1e300 * 1e10, -1e300 * 1e10, 5e-324, 2.5e-310, nz, 1e15, 1e16, -1e-7, 1/3, 100, -0.25)
print(123456789012345 .. "", 123456789012355 .. "", 123456789012365 .. "", 2^53 + 2 .. "", 987654321098765 .. "")
print(string.len(123456789012345), ("x"):rep(2) .. 12345678901234.5, string.upper(-nan))
local t = {}
for i = 1, 30 do t[#t + 1] = tostring(i * 1e14 + 5) end
print(table.concat(t, " "))
