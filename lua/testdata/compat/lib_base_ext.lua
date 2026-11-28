-- PUC Lua 5.1: load takes a reader function only, loadstring a string only;
-- assert's message must be a string; no collectgarbage("isrunning");
-- xpcall ignores the arguments after the handler; tonumber(s, base) of a
-- negative number is strtoul's value modulo 2^64 (LuaJIT's are in the
-- *.jit.lua scripts)
local function err(f, ...)
  local ok, e = pcall(f, ...)
  return ok, type(e) == "string" and e:match("%(([^()]*)%)$") or e
end
print(loadstring == load, err(load, "x = 1"))
print(err(loadstring, function() return nil end))
local parts, i = {"return ", 1, " + ", "2"}, 0
print(load(function() i = i + 1; return parts[i] end, "=reader", "b")())
print(err(function() assert(false, {}) end))
print(err(function() assert(nil, nil) end))
print(err(collectgarbage, "isrunning"))
print(xpcall(function(...) return select("#", ...), ... end, print, 1, 2, 3))
print(tonumber("-1", 2), tonumber("-10", 16), tonumber("-ffffffffffffffff", 16), tonumber("-1ffffffffffffffff", 16), tonumber("-z", 36))
