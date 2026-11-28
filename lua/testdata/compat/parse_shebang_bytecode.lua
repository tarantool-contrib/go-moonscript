-- Lua 5.1's loadstring skips no "#" line and no BOM (luaL_loadfile skips
-- the line of a file); LuaJIT's lexer does, and refuses bytecode after
-- them (parse_shebang_bytecode.jit.lua).
local function try(name, src, chunk)
  local f, err = loadstring(src, chunk or "=src")
  if f then
    print(name, "ok", f())
  else
    print(name, (err:gsub("^[^:]*:", "")))
  end
end
try("shebang bytecode", "#!lua\n\27LJ")
try("shebang Lua bytecode", "#!lua\n\27Lua")
try("bom bytecode", "\239\187\191\27LJ")
try("bom shebang bytecode", "\239\187\191#!x\n\27LJ")
try("shebang only", "#!lua")
try("shebang code", "#!lua\nreturn 1")
try("bom code", "\239\187\191return 2")
try("bytecode in the line", "#\27")
try("cr", "#!lua\r\27LJ")
try("file name", "#!lua\n\27LJ", "@file.lua")
