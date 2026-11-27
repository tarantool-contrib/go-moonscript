-- Lua 5.1 keeps a record of every local variable a function declares
-- (f->locvars, for debug info), at most SHRT_MAX = 32767: the 32768th,
-- even in a block long closed, fails in luaM_growvector with "too many
-- local variables", raised by luaG_runerror without a position (the
-- loader is a C function). Parameters, "self", the implicit "arg" and the
-- hidden variables of a for loop have records too. LuaJIT 2.1 has no such
-- limit (parse_locvars_limit.jit.lua: the same script for luajit).
-- loadstring is called under pcall: lua.c's traceback handler would get
-- the message of a runtime error otherwise.
local r = string.rep
local function decls(n)
  local b = {}
  while n > 0 do
    local k = n > 100 and 100 or n
    b[#b + 1] = "do local " .. r("a,", k - 1) .. "a end\n"
    n = n - k
  end
  return table.concat(b)
end
local function try(name, src)
  local ok, f, err = pcall(loadstring, src, "=src")
  if f then
    print(name, "ok")
  else
    print(name, ok, (string.gsub(err, "^[^:]*:", "")))
  end
end
try("main 32767", decls(32767))
try("main 32768", decls(32768))
try("function 32767", "local function f(p, q)\n" .. decls(32765) .. "end")
try("function 32768", "local function f(p, q)\n" .. decls(32766) .. "end")
try("vararg 32767", "local function f(...)\n" .. decls(32766) .. "end")
try("vararg 32768", "local function f(...)\n" .. decls(32767) .. "end")
try("method 32768", "local t = {} function t:m()\n" .. decls(32767) .. "end")
try("numeric for", decls(32763) .. "for i = 1, 2 do end")
try("numeric for over", decls(32764) .. "for i = 1, 2 do end")
try("generic for", decls(32762) .. "for k, v in next, {} do end")
try("generic for over", decls(32763) .. "for k, v in next, {} do end")
try("local function", decls(32767) .. "local function g() end")
try("nested apart", decls(32000) .. "local function g()\n" .. decls(32000) .. "end\n" .. decls(700))
try("before an error", decls(32768) .. "x = = 1")
try("after an error", "x = = 1\n" .. decls(32768))
local ok, f = pcall(loadstring, decls(32767) .. "return 'run'")
print("runs", f and f())
