-- The cases of lib_string_format.lua under DialectLuaJIT, compared with luajit's output.
-- string.format as Lua 5.1's str_format: C's sprintf conversions with a
-- long, an unsigned long, a double, a char; %s, %q; errors
local specs = {"%d", "%i", "%5d", "%-5d|", "%05d", "%+d", "% d", "%.3d", "%8.3d", "%08.3d",
  "%-05d", "%.0d", "%u", "%+u", "%o", "%#o", "%#.3o", "%x", "%X", "%#x", "%#X", "%#.0x",
  "%#08x", "%08.3x", "%-#8x|", "%+x", "% x", "%c", "%5c|", "%-5c|", "%e", "%E", "%.0e",
  "%#.0e", "%+.3e", "%010.3e", "%-10.3e|", "%f", "%.0f", "%#.0f", "%5.2f", "%-8.3f|",
  "% f", "%010f", "%g", "%G", "%#g", "%.3g", "%#.3g", "%.0g", "%.14g", "%.17g", "%+g",
  "% g", "%-8g|", "%08g", "%s", "%5s", "%-5s|", "%.2s", "%5.1s|", "%05s", "%.0s", "%q", "%5q"}
local vals = {0, 1, -1, 65, 255, 3.75, -2.5, 0.5, 1.5, 1e10, 123456789, 0.000123, 1e-5,
  1e20, 1e100, 1/3, "12", " 0x10 ", 2^53, 2^63, -2^63, 2^64, 1/0, -1/0}
for _, sp in ipairs(specs) do
  local out = {}
  for _, v in ipairs(vals) do
    local ok, r = pcall(string.format, sp, v)
    out[#out+1] = ok and ("[" .. r .. "]") or "error"
  end
  print(sp, table.concat(out, " "))
end
-- a negative zero, NaNs
local z = 0
z = -z
local nan = 0/0
print(string.format("%g %e %f %d %s %.1f", z, z, z, z, z, -z))
print(string.format("%s %s %f %g %5.1e|%-6g|%+g", nan, -nan, nan, -nan, nan, nan, -nan))
print(string.format("%e %G %f %+g % g %08g %-6f|", 1/0, -1/0, 1/0, 1/0, 1/0, -1/0, 1/0))
-- %s takes strings and numbers, cut at a NUL unless 100 bytes or longer
print(string.format("%s|%s|%s", 12, 0.1, 1e15), #string.format("%s", "a\0b"))
local long = string.rep("x", 60) .. "\0" .. string.rep("y", 60)
print(#string.format("%s", long), #string.format("%.9s", long), #string.format("%5s", "a\0b"))
print(string.format("%3s|%-3s|%.1s", "\195\169", "\195\169", "\195\169") == "  \195\169|\195\169 |\195")
-- %c is a byte, a NUL ends the item
print(#string.format("%c", 0), #string.format("a%cb", 0), #string.format("%3c", 0),
  #string.format("%-3c", 0), string.format("%c", 256 + 65), #string.format("%c", 2^40))
-- %q
print(string.format("%q", "a\nb\r\0c\"\\\1\255"))
print(string.format("%q|%q|%q", 1/3, 42, 1e100))
print(string.format("%q", string.rep("x", 150)) == '"' .. string.rep("x", 150) .. '"')
for i = 0, 255 do
  local s = "a" .. string.char(i) .. "b"
  assert(loadstring("return " .. string.format("%q", s))() == s)
end
-- plain text, %%, arguments
print(string.format("no conversions"), string.format("100%%"), string.format("%%d", 5),
  string.format("%d%%", 5), string.format("%s %s", 1, 2, 3))
print(string.format("%5.2f%%|%-3d|%3d", 99.555, 7, 7))
-- errors: the messages of luaL_error (luaL_argerror ones: only that they fail)
local function try(...)
  local args = {...}
  local n = select("#", ...)
  local ok, err = pcall(function() local s = string.format(unpack(args, 1, n)) return s end)
  print(ok, err)
end
try("%y", 1)
try("%", 1)
try("%1234d", 1)
try("%.123f", 1)
try("%-+ #0-d", 1)
try("%-+ #0d", 1)
try("%10.10f", 1)
try("%l", 1)
try("%ld", 1)
try("%n", 1)
try("%p", 1)
for _, args in ipairs({{"%d"}, {"%d", "x"}, {"%d", {}}, {"%s"}, {"%s", nil}, {"%s", true},
    {"%s", {}}, {"%q", {}}, {"%c", "x"}, {"%f", "1e"}, {"%x", nil}, {"%d %d", 1}}) do
  print((pcall(string.format, unpack(args, 1, 2))))
end
print(string.format("%d %x %.1f", "10", "0x10", "1e1"))
