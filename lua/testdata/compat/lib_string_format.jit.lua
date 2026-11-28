-- LuaJIT 2.1's string.format (lj_strfmt): exact ties rounded away from
-- zero, NaN without sign, %F, %a/%A, %p, %s/%q of any value, %q with
-- \ddd escapes, 64-bit integer conversions, its error messages
local function f(...)
  local ok, r = pcall(string.format, ...)
  if ok then return "[" .. r .. "]" end
  return "error: " .. (r:match("invalid option '.*' to 'format'") or r:match("%(([^()]*)%)$") or r)
end
local z = 0
local nz = -z
print(f("%.0f %.0f %.0f %.0f %.0f %.0f", 0.5, 1.5, 2.5, -0.5, -2.5, 4.5))
print(f("%.2f %.1f %.3f %.2f %.2f", 0.125, 0.25, 1.0005, 0.375, -0.125), f("%.1e %.0e %.0e", 2.5e10, 5e-1, 2.5))
print(f("%.13g", 1234567890123.5), f("%.3g", 2.5e-5), f("%.5g|%.1e|%.1g", 2.5, 0.25, 9.5), f("%.19e", 2^-30), f("%.0f", 2^70 + 2^18))
print(f("%.14g|%.14g|%.15g|%.0f|%.1f", 0.5, 2.5e-7, 0.1, 1e21, 0.05), f("%.20f|%.17g", 0.1, 0.1), f("%.30e", 1/3))
print(f("%5.2f|%-8.3e|%+g|% g|%#g|%#.0f|%010.3f|%#.0e|%#x|%#o", 1.005, 12345.6789, 1, 1, 1, 1, -1.5, 3, 255, 8))
print(f("%g %g %g %g %g %g", 1e-5, 1e-4, 1e15, 1e16, 0.5, 100000), f("%.0g|%e|%g|%G|%E", 0, 0, nz, 1e-20, 1e20))
print(f("%f %F %e %g %5.1f| %-6g| %+f %05f %E %G", 0/0, 1/0, -1/0, -(0/0), 0/0, 1/0, 1/0, -1/0, 0/0, 1/0))
print(f("%+f|% f|%+5.1f|%-6f|%06f|% e|%+e|%08.2e|%-9g|", 0/0, 0/0, 0/0, 0/0, 0/0, 1/0, 1/0, -1/0, 1/0))
print(f("%a %a %a %A %.3a %a %a %a", 1, 0.5, -0.1, 255.5, 1/3, 0, 1/0, nz))
print(f("%a|%a|%10.2a|%-12a|%+a|%#a|%.1a|%.1a|%.0a|%.0a|%012a|% a", 5e-324, 2^-1030, 1, 1, 1, 1, 1 + 2^-5, 1 + 3*2^-5, 1.5, 1.96875, 1, 2))
print(f("%d %d %d %i %d %d %d", 2^31, -2^31, 2^53, -3.7, 3.5, -3.5, 2.5), f("%5.3d|%.3x|%.0d|%5.0d|%#.0x|%#x", 7, 7, 0, 0, 0, 0))
print(f("%x %X %o %u %u %x", 255.5, 3e9, -8, -1, 2^32, -1), f("%d %d %d %d %x %x", 2^63, -2^63, 2^64, 1e30, -2^63, 2^63))
print(f("%x %x %d %d %d %d", 2^64 + 2^60, -1e30, 4503599627370496.5, -0.9, 1/0, 0/0), f("%5d|%x|%i|%o|%X", 0/0, 0/0, 2^62, 2^62, 2^62))
print(f("%-+ #0d|%-#20.3x|%+.3d|% 05d|%-5d|%05d", 3, 255, 7, 7, -3, -3), f("%c%c|%5c|%-3c|%.3c|%c", 104, 105, 65, 66, 65, 321))
print(f("%s|%5s|%-5s|%.1s|%5.1s|%s|%s", 1.5, true, nil, "abc", 1.5, 2^63, 1e15), f("%s", setmetatable({}, {__tostring = function() return "TS" end})))
print(f("%s", "a\0b") == "[a\0b]", f("%5s", "a\0b") == "[  a\0b]", f("%.2s", "a\0b") == "[a\0]")
print(f("%q", "\0\0011\1a\r\n\t\"\\\127\255x"), f("%q", "\0009"), f("%q", "a\0"), f("%q", "\r\t\v\f\b\a\27"), f("%q", "\200\31\0319"))
print(f("%q|%q|%q|%q|%10.4q", 1.5, true, 2^63, nil, "x"), f("%q", setmetatable({}, {__tostring = function() return "T\n" end})))
print(f("%p", 1), f("%p", nil), f("%p", true), f("%5.1p", false), f("%p", {}):match("^%[0x%x+%]$") ~= nil, f("%p", print):match("^%[0x%x+%]$") ~= nil)
print(f("%5"), f("%100d", 1), f("%.100f", 1), f("%100s", "x"), f("%1.100s", "x"), f("%5%"), f("%ld", 3), f("%y", 1))
print(f("abc%"), f("%."), f("%.3"), f("%10"), f("%-"), f("%3$s", 1), f("%n", 1), f("%hd", 1), f("%Lf", 1), f("%ss", 1))
print(f("%d"), f("%s %s", 1), f("%d", {}), f("%x", "z"), f("%d", "0x10"), f("%c", "65"), f("%f", "1e2"), f("%%|%5.1f%%", 2.25))
print(f("%99.99f", 1):len(), f("%099d", 1):len(), f("%.99d", 1):len())
