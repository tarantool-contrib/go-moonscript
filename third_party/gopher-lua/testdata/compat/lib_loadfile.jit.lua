-- loadfile and dofile are luaL_loadfile's: chunk name "@" + the file name,
-- "cannot open <file>: <strerror>" / "cannot read <file>: <strerror>"
-- (dofile raises it as it is), nil (not "") for the standard input, a
-- first line starting with '#' skipped with the line numbers kept; under
-- Lua 5.1 a binary chunk after that line loads, LuaJIT refuses it (the
-- .jit.lua copy has luajit's results). The files are in files/.
print("missing", loadfile("files/nonexist.lua"))
print("missing dofile", pcall(dofile, "files/nonexist.lua"))
print("missing dir", loadfile("files/nodir/x.lua"))
print("directory", loadfile("files"))
print("directory dofile", pcall(dofile, "files"))
print("empty name", loadfile(""))
print("empty name dofile", pcall(dofile, ""))
print("not a string", pcall(loadfile, {}))
print("not a string dofile", pcall(dofile, {}))
print("number name", loadfile(42))

print("results", select("#", dofile("files/ret3.lua")), dofile("files/ret3.lua"))
do -- an empty file (made here: the patch of the vendored tree cannot hold one)
  local name = os.tmpname()
  assert(io.open(name, "w")):close()
  print("empty", pcall(dofile, name))
  os.remove(name)
end
local f = loadfile("files/err.lua")
print("source", debug.getinfo(f, "S").source, debug.getinfo(f, "S").short_src)
print("runtime error", pcall(f))
print("runtime error dofile", pcall(dofile, "files/err.lua"))

-- "#" lines
print("shebang", pcall(dofile, "files/shebang.lua"))
print("shebang syntax error", loadfile("files/shebang_err.lua"))
print("shebang syntax error dofile", pcall(dofile, "files/shebang_err.lua"))
print("shebang runtime error", pcall(dofile, "files/shebang_rt.lua"))
print("shebang only", pcall(dofile, "files/shebang_only.lua"))

-- a binary chunk after a "#" line (written here: string.dump's chunks are
-- the VM's own)
local name = "files/tmp_shebang_dump.out"
local function write(s)
  local out = assert(io.open(name, "wb"))
  out:write(s)
  out:close()
end
local dump = string.dump(function() return "from the dump" end)
write(dump)
print("binary", pcall(dofile, name))
write("#!/usr/bin/lua\n" .. dump)
local g, err = loadfile(name)
print("shebang binary", g and g() or (err:gsub("^[^:]*: ", "")))
write("#!/usr/bin/lua")
print("shebang no newline", pcall(dofile, name))
write("#!/usr/bin/lua\n\nreturn debug.getinfo(1, 'l').currentline")
print("shebang lines", pcall(dofile, name))
os.remove(name)
