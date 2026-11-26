-- the metatable of strings is a table of its own, whose __index is string
local mt = getmetatable("")
print(type(mt), mt == string, mt.__index == string, rawget(string, "__index"))
print(getmetatable("abc") == mt, ("x"):rep(3), ("%d"):format(5), #("abc"):upper())
local n = 0
for k in pairs(mt) do n = n + 1 end
print(n)
function string.twice(s) return s .. s end
print(("ab"):twice())
mt.__index = function(s, k) return k .. "!" end
print(("ab").foo)
mt.__index = string
print(pcall(function() return ("x"):nosuchmethod() end) == false)
