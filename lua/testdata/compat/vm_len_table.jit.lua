-- The length operator ignores __len for tables in LuaJIT 2.1 too (built
-- without LUAJIT_ENABLE_LUA52COMPAT).
local t = setmetatable({1, 2}, {__len = function() return 42 end})
print(#t)
local s = setmetatable({}, {__len = function() return 42 end})
print(#s)
print(pcall(function() return #setmetatable({}, {__len = 1}) end))
