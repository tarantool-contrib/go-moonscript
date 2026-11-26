-- LuaJIT 2.1 extension: load(chunk [, chunkname [, mode [, env]]]) takes a
-- string chunk, a mode and an environment
print(type(load("return 1 + 1")), load("return 1 + 1")())
print(load(42) == nil)
print((load("syntax error here")))
print(load("return 2", "=x", "b"))
print(load("return 2", "=x", "t")(), load("return 3", "=x", "bt")())
print(load("return x", "=x", "t", {x = 5})())
print(load("return y", nil, nil, {y = 6})())
x = "global"
print(load("return x")(), load("return x", "=x", nil, 42)())
local parts = {"return ", "3"}
local i = 0
print(load(function() i = i + 1 return parts[i] end)())
i = 0
print(load(function() i = i + 1 return parts[i] end, "=r", "t", {})())
print(load(function() return nil end, "=r", "b"))
print((pcall(load)), (pcall(load, {})))
