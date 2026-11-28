-- LuaJIT's lexer (lj_lex_setup) skips a UTF-8 BOM and a first line
-- starting with "#" in any chunk, loadstring's too, and refuses bytecode
-- after them with "cannot load malformed bytecode", the chunk name not
-- echoed; the mode of load and loadfile is checked after that. loadfile
-- (luaL_loadfilex) reads the file as it is and leaves the line to the
-- lexer. Lua 5.1's loadstring skips nothing (parse_shebang_bytecode.lua).
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
try("crlf", "#!lua\r\n\27LJ")
try("blank line", "#!lua\n\n\27LJ")
try("file name", "#!lua\n\27LJ", "@file.lua")
try("line numbers", "#!lua\nreturn 1 +")
print("load b", load("#!lua\n\27LJ", "=src", "b"))
print("load t", load("#!lua\n\27LJ", "=src", "t"))
print("load bt", load("#!lua\n\27LJ", "=src", "bt"))
print("load b text", load("#!lua\nreturn 1", "=src", "b"))
local dump = string.dump(function() return "dumped" end)
local name = os.tmpname()
local function file(text)
  local f = assert(io.open(name, "wb"))
  f:write(text)
  f:close()
end
local function clean(s) return (tostring(s):gsub(name:gsub("%p", "%%%0"), "FILE")) end
for _, c in ipairs({
  {"dump", "#!lua\n" .. dump},
  {"bytecode", "#!lua\n\27LJ"},
  {"text", "#!lua\nreturn 'text'"},
  {"bom", "\239\187\191#!lua\nreturn 'bom'"},
  {"two lines", "#!lua\n#second\nreturn 'two'"},
  {"crlf", "#!lua\r\nreturn 'crlf'"},
}) do
  file(c[2])
  local f, err = loadfile(name)
  print("loadfile " .. c[1], f and f() or clean(err))
  print("loadfile b " .. c[1], clean(select(2, loadfile(name, "b"))))
  local ok, r = pcall(dofile, name)
  print("dofile " .. c[1], ok, clean(r))
end
os.remove(name)
