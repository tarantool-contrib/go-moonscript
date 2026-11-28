-- LuaJIT 2.1: coroutine.isyieldable, package.searchpath, debug.upvalueid,
-- debug.upvaluejoin, debug.getinfo's "u" nparams and isvararg,
-- debug.getlocal(f, n), os.date's E and O modifiers
print(coroutine.isyieldable(), coroutine.isyieldable(1, 2), coroutine.wrap(function() return coroutine.isyieldable() end)())
print(coroutine.wrap(function() local r; table.sort({3, 2, 1}, function(a, b) r = coroutine.isyieldable(); return a < b end); return r end)())
print(coroutine.wrap(function() local r; string.gsub("a", "a", function() r = coroutine.isyieldable() end); return r end)())
print(coroutine.running(), select("#", coroutine.running()))
print(package.searchpath("nonexistent", "/nonexistent/?.lua;/nonexistent2/?/init.lua"))
print(package.searchpath("a.b.c", "/n/?.lua", "", ""))
print(package.searchpath("a.b", "/n/?.lua", "b", "Z"))
print(package.searchpath("a_b", "/n/?.lua", "_"))
print(package.searchpath("etc", "/nonexistent/?;;/?"))
print(package.searchpath("x", ""), package.searchpath("x", ";;"))
print(package.searchpath(1, 2))
print((select(2, pcall(package.searchpath)):match("#2.*string expected")) ~= nil)
local a, b = 1, 2
local function fa() return a end
local function fa2() return a end
local function fb() return b end
local function fab() return a, b end
print(type(debug.upvalueid(fa, 1)), debug.upvalueid(fa, 1) == debug.upvalueid(fa2, 1), debug.upvalueid(fa, 1) == debug.upvalueid(fb, 1))
print(debug.upvalueid(fab, 1) == debug.upvalueid(fa, 1), debug.upvalueid(fab, 2) == debug.upvalueid(fb, 1), debug.upvalueid(fab, "2") == debug.upvalueid(fab, 2.5))
print(select("#", debug.upvaluejoin(fa, 1, fb, 1)), fa(), fa2(), debug.upvalueid(fa, 1) == debug.upvalueid(fb, 1))
b = 20
print(fa(), fb(), a)
local function argerr(f, ...)
  local ok, e = pcall(f, ...)
  return ok, e:match("%(([^()]*)%)$")
end
print(argerr(debug.upvalueid, fa, 2), argerr(debug.upvalueid, fa, 0), argerr(debug.upvalueid, 1, 1))
print(argerr(debug.upvaluejoin, print, 1, fa, 1), argerr(debug.upvaluejoin, fa, 1, print, 1), argerr(debug.upvaluejoin, fa, 1, fb, 2), argerr(debug.upvaluejoin, fa, 3, fb, 1))
local i1 = debug.getinfo(function(x, y, ...) end, "u")
local i2 = debug.getinfo(function(x) end, "u")
local i3 = debug.getinfo(print, "u")
print(i1.nups, i1.nparams, i1.isvararg, i2.nparams, i2.isvararg, i3.nparams, i3.isvararg, i1.func, i1.source)
print(debug.getinfo(1, "u").nparams, debug.getinfo(1, "u").isvararg, (function(p, q) return debug.getinfo(1, "u").nparams end)())
local i4 = debug.getinfo(fab)
print(i4.nparams, i4.isvararg, i4.func == fab, i4.nups)
print(debug.getlocal(function(x, y, ...) local z end, 1), debug.getlocal(function(x, y) local z end, 2), debug.getlocal(function(x, y) local z end, 3), debug.getlocal(print, 1))
print(debug.getlocal(function() end, 1), (function(p) local n, v = debug.getlocal(1, 1); return n, v end)("v"))
print(os.date("!%Ey|%Oy|%EC|%Od|%E|%O|%Ez|%OH|%OS|%Ex|%EX|%Ec|%Ob", 86400 * 40 + 3600 * 15 + 61))
print(os.date("!%Ea|%Oa|%Ed|%Ej|%EE|%EOy|%%Ey|%E%|%O%", 86400 * 40 + 3600 * 15 + 61))
print(os.date("!%E", 0), os.date("!x%O", 0))
