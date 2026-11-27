-- A NUL byte in the source where Lua 5.1's lexer checks the next character
-- against a set (check_next: "." after ".", "Ee" and "+-" in a numeral)
-- is taken as one of the set: strchr finds the set's terminating NUL. So
-- "1\0garbage" is a numeral (read_numeral reads it through the exponent
-- check, luaO_str2d converts the C string "1") and "a.\0b" is a .. b.
-- LuaJIT compares characters: both are errors there
-- (parse_nul_byte.lua: the same script for lua5.1).
local function try(name, src)
  local f, err = loadstring(src, "=src")
  if not f then
    print(name, (err:gsub("^[^:]*:", "")))
    return
  end
  a, b = "A", "B"
  print(name, pcall(f))
end
try("number", "return 1\0garbage")
try("number sign", "return 1\0+5")
try("number two nul", "return 1.5\0\0")
try("number then x", "return 0\0x5")
try("number then e", "return 3\0e5")
try("number dot", "return .5\0z")
try("number at end", "return 1\0")
try("exponent nul", "return 1e\0")
try("exponent nul digits", "return 1e\0x5")
try("exponent sign nul", "return 1e+\0")
try("hex nul", "return 0x\0")
try("hex digits nul", "return 0x1\0p4")
try("dot", "return a.\0b")
try("dot at end", "return a.\0")
try("dots", "return a..\0b")
try("dot dot nul", "return a.\0.b")
try("field", "return a.\0")
try("name", "return a\0b")
try("string", "return 'a\0b' == 'a' .. '\0' .. 'b'")
try("comment", "return 1 -- \0 x")
