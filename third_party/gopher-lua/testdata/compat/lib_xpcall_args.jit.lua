-- LuaJIT 2.1 extension: xpcall(f, handler, ...) passes the arguments to f
local function h(e) return "handled: " .. tostring(e) end
print(xpcall(function(...) return select("#", ...), ... end, h, 1, nil, 3))
print(xpcall(function(a, b) return a + b end, h, 2, 3))
print(xpcall(function(a) error(a, 0) end, h, "arg"))
print(xpcall(setmetatable({}, {__call = function(self, a) return a end}), h, "callable"))
print(xpcall(print, h))
local result
xpcall(function(arg) result = arg end, h, 9)
print(result)
