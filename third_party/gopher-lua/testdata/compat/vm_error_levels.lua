-- Lua 5.1's error(): level 2 is the position of the caller of the function
-- that called error (was the function itself), a level that isn't a Lua
-- function with a line (a Go function, a level lost to a tail call, past
-- the bottom) adds no position, and then no space either (was " x"); any
-- value can be raised (error() raises nil; was "value expected"), a number
-- gets the position like a string (and becomes one), other values and
-- level 0 are raised as they are.
local function p(...) print(pcall(...)) end
p(function() error("x") end)
p(function() error("x", 1) end)
p(function() error("x", 2) end)
local function lvl2() error("lvl2", 2) end
p(function()
  lvl2()
end)
local function lvl3() error("lvl3", 3) end
local function mid()
  lvl3()
end
p(function()
  mid()
end)
p(function() error("x", 3) end)
p(function() error("x", 50) end)
p(function() error("x", 0) end)
p(function() error("x", -1) end)
p(function() error("x", 1.5) end)
p(function() error("x", "2") end)
p(function() error(42) end)
p(function() error(42, 0) end)
p(function() error(1.5) end)
p(function() error(1e100) end)
p(function() error(1/0) end)
p(function() print(type(select(2, pcall(error, 42)))) end)
p(function() print(type(select(2, pcall(error, 42, 0)))) end)
p(function() local ok, e = pcall(error, {}) print(type(e)) end)
p(function() error() end)
p(function() error(nil) end)
p(function() error(true) end)
p(function() error(false, 2) end)
p(error)
p(error, "msg")
p(error, "msg", 2)
p(error, "msg", 0)
local co = coroutine.create(function() error("in co", 2) end)
print(coroutine.resume(co))
co = coroutine.create(function() local function f() error("in co2", 2) end f() end)
print(coroutine.resume(co))
co = coroutine.create(function() error("in co3") end)
print(coroutine.resume(co))
local function tc() return error("tail", 2) end
p(function() tc() end)
local function tc2() error("tail2", 2) end
local function tc3() return tc2() end
p(function() tc3() end)
local function tc4() error("tail4", 3) end
local function tc5() tc4() end
local function tc6() return tc5() end
p(function() tc6() end)
-- the messages of other Go functions too have no leading space
p(function() local t = setmetatable({}, {__index = function() error("idx", 2) end}) return t.x end)
p(function() local t = setmetatable({}, {__index = function() error("idx", 3) end}) return t.x end)
p(string.rep)
