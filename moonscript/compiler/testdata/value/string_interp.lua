local a = "hello " .. tostring(name) .. "!"
local b = tostring(x)
local c = ""
local d = 'plain'
local e = ("a" .. tostring(b)) .. c
local f = tostring(a + b) .. " and " .. tostring(c or d)
local g = "x " .. tostring(y) == z
local h = [[long #{x}]]
