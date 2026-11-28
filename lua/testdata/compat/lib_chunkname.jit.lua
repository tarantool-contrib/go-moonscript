-- Chunk names: what loadstring, load, loadfile and dofile name a chunk
-- (lua_load's chunkname: "@file", "=name", the source text), which
-- debug.getinfo reports as source, and the id error messages show for it
-- (luaO_chunkid, short_src). The .jit.lua copy has luajit's results, which
-- shorten long names a little differently (lj_debug_shortname).
local function show(label, f)
  if type(f) ~= "function" then return print(label, "load error", f) end
  local i = debug.getinfo(f, "S")
  print(label, string.format("%q", i.source), i.short_src, i.what)
end
local function src(s) return (s:gsub("\n", "\\n")) end

show("main", debug.getinfo(1, "f").func)
show("local function", show)
show("print", print)
show("loadstring", loadstring("return 1"))
show("loadstring =name", loadstring("return 1", "=named"))
show("loadstring @file", loadstring("return 1", "@file.lua"))
show("loadstring name", loadstring("return 1", "chunk"))
show("loadstring lines", loadstring("x = 1\nreturn 2"))
show("loadstring cr", loadstring("x = 1\rreturn 2"))
show("loadstring tab", loadstring("x = 1\treturn 2"))
show("loadstring empty name", loadstring("return 1", ""))
for _, n in ipairs({ 40, 42, 43, 44, 45, 46, 47, 48, 49, 60 }) do
  show("loadstring " .. n, loadstring("return " .. string.rep("x", n - 7)))
end
for _, n in ipairs({ 50, 52, 53, 55, 56, 57, 59, 60, 61, 80 }) do
  show("@path " .. n, loadstring("return 1", "@" .. string.rep("d/", (n - 5) / 2) .. "f.lua"))
end
for _, n in ipairs({ 58, 59, 60, 70 }) do
  show("=name " .. n, loadstring("return 1", "=" .. string.rep("n", n)))
end
show("load", load(function() return nil end))
show("load =ld", load(function() return nil end, "=ld"))
local pieces = { "local a = 1\n", "return a" }
show("load pieces", load(function() return table.remove(pieces, 1) end, "pieces"))
show("loadfile", loadfile("files/ret3.lua"))
show("loadfile ./", loadfile("./files/ret3.lua"))
show("nested", loadstring("return function() end", "=outer")())
show("dumped", loadstring(string.dump(loadstring("return 1", "@dumped.lua"))))
show("dumped nested", loadstring(string.dump(loadstring("return function() end", "=outer")))())

-- error positions of runtime errors and syntax errors
local function run(label, f, ...)
  if type(f) ~= "function" then return print(label, "load error", f) end
  print(label, pcall(f, ...))
end
run("error", loadstring("error('boom')"))
run("error =name", loadstring("error('boom')", "=named"))
run("error @file", loadstring("error('boom')", "@file.lua"))
run("error name", loadstring("error('boom')", "chunk"))
run("error lines", loadstring("local x = 1\nerror('boom')"))
run("error long", loadstring("error('boom') --" .. string.rep("-", 50)))
run("error load", load(function() return nil end))
run("error nested", loadstring("local function f() error('deep') end f()", "=nested"))
run("dumped error", loadstring(string.dump(loadstring("local x\nerror('E')", "=dumped"))))
print("syntax", loadstring("x = = 1"))
print("syntax =name", loadstring("x = = 1", "=named"))
print("syntax @file", loadstring("x = = 1", "@file.lua"))
print("syntax name", loadstring("x = = 1", "chunk"))
print("syntax eof", loadstring("x = ("))
print("syntax lines", loadstring("x = 1\ny = = 2"))
local bad = { "x = ", "= 1" }
print("syntax load", load(function() return table.remove(bad, 1) end, "=l"))
print("syntax file", loadfile("files/syn.lua"))
-- (Lua 5.1's lexer shows ids of up to 80 bytes, MAXSRC; LuaJIT's 60)
for _, n in ipairs({ 60, 63, 64, 70, 90 }) do
  print("syntax long " .. n, loadstring("x = = 1 --" .. string.rep("-", n - 10)))
end
for _, n in ipairs({ 60, 72, 73, 90 }) do
  print("syntax @path " .. n, loadstring("x = = 1", "@" .. string.rep("d/", (n - 5) / 2) .. "f.lua"))
end
print("syntax =name 90", loadstring("x = = 1", "=" .. string.rep("n", 90)))
print("limit error long", loadstring(string.rep("local a ", 201) .. " --" .. string.rep("-", 60)))
-- the reader of load: its errors are load's, its wrong values an error at
-- the caller of load
local function readers()
  print("reader error", load(function() error("rdr") end))
  print("reader error value", select("#", load(function() error({}) end)))
  local f, err = load(function() return {} end)
  print("reader value", f, err)
  local n = 0
  f, err = load(function() n = n + 1 if n == 1 then return 12 end end)
  print("reader number", f, err)
end
print(pcall(readers)) -- (protected: lua.c's traceback handler is load's too)
print("reader at pcall", pcall(load, function() return true end))

-- binary chunks are named by luaU_undump without '@' or '=', "binary
-- string" when named by themselves
print("binary", loadstring("\27Lua garbage"))
print("binary =name", loadstring("\27Lua garbage", "=bin"))
print("binary @file", loadstring("\27Lua garbage", "@bin"))
print("binary name", loadstring("\27Lua garbage", "named"))
