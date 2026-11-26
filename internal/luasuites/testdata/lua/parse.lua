-- Prints for every path read from standard input (relative to the directory
-- given as the argument) whether loadstring accepts the file:
-- "<path>\tok" or "<path>\terror\t<message>". A first line starting with
-- "#" is emptied first, as lua.c's loadfile skips it. Run on lua5.1 and
-- luajit for the parse check's references (parse_test.go does the same on
-- our VM).
local root = assert(arg[1], "usage: parse.lua <dir> < paths")
for path in io.lines() do
  local f = assert(io.open(root .. "/" .. path, "rb"))
  local src = f:read("*a")
  f:close()
  if src:sub(1, 1) == "#" then
    src = src:gsub("^[^\n]*", "", 1)
  end
  local fn, err = loadstring(src, "=src")
  if fn then
    print(path .. "\tok")
  else
    print(path .. "\terror\t" .. (err:gsub("[\r\n\t]", " ")))
  end
end
