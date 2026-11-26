-- xpcall(f, handler) as in Lua 5.1: any callable f, any handler value
local function h(e) return "handled: " .. tostring(e) end
print(xpcall(function() return 1, 2, 3 end, h))
print(xpcall(function() error("boom", 0) end, h))
print(xpcall(function() error({}) end, function(e) return type(e) end))
print(xpcall(setmetatable({}, {__call = function(self) return 42 end}), h))
print(xpcall(setmetatable({}, {__call = function() error("boom", 0) end}), h))
print(xpcall(nil, h))
print(xpcall({}, h))
print(xpcall(42, function(e) return e end))
-- a handler that is not a function: "error in error handling"
print(xpcall(function() return "ok" end, nil))
print(xpcall(function() return "ok" end, 42))
print(xpcall(function() error("x", 0) end, nil))
print(xpcall(function() error("x", 0) end, 42))
print(xpcall(function() error("x", 0) end, setmetatable({}, {__call = h})))
print(xpcall(nil, nil))
print(select("#", xpcall(function() error("x", 0) end, function() end)))
print((pcall(xpcall, function() return 1 end)))
print(pcall(xpcall, function() return 1 end, h))
-- the handler runs before the stack unwinds
print(xpcall(function() local function inner() error("deep", 0) end inner() end,
  function(e) return e .. " " .. type(debug.traceback()) end))
