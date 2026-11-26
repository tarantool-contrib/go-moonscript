-- base and table library details of Lua 5.1: tostring and __tostring,
-- setmetatable's checks, getfenv/setfenv levels, string.char's range,
-- table.insert/concat errors, gcinfo, table.foreach/foreachi/getn/setn,
-- math.mod, string.gfind, # ignoring __len on tables (print is the test's)
local pcall0 = pcall
local function pcall(...)
  -- (positions of errors raised in functions pcall calls directly, the
  -- wording of argument errors and of VM errors are the errors area's)
  local r = {pcall0(...)}
  if r[1] == false and type(r[2]) == "string" then
    r[2] = r[2]:gsub("^[^:]*:%d+: ", ""):gsub("to '%?'", "to ?"):gsub("to %(anonymous%)", "to ?")
      :gsub("attempt to call a non%-function object", "attempt to call a string value")
  end
  return unpack(r, 1, select("#", ...) + 2)
end
local function addr(...)
  local t = {...}
  for i = 1, select("#", ...) do t[i] = tostring(t[i]):gsub("0x%x+", "0x?") end
  return unpack(t, 1, select("#", ...))
end
local t = setmetatable({}, {__tostring = function() end})
print(select("#", tostring(t)), tostring(t))
t = setmetatable({}, {__tostring = function() return 42 end})
print(tostring(t), type(tostring(t)))
t = setmetatable({}, {__tostring = function() return {} end})
print(type(tostring(t)))
t = setmetatable({}, {__tostring = function() return "a", "b" end})
print(tostring(t))
t = setmetatable({}, {__tostring = "not a function"})
print(pcall(tostring, t))
t = setmetatable({}, {__tostring = setmetatable({}, {__call = function(_, x) return "called" end})})
print(pcall(tostring, t))
local mt = {__index = function() return "x" end}
print(pcall(setmetatable, 1, {}), pcall(setmetatable, "s", {}), pcall(setmetatable, {}, 1))
print(pcall(setmetatable, setmetatable({}, {__metatable = "locked"}), {}))
print(addr(pcall(setmetatable, {}, nil)))
print(pcall(next, {}, "nokey"), pcall(next, {a = 1}, "b"), next({}, nil))
print(pcall(getfenv, 100), pcall(getfenv, -1), pcall(setfenv, 100, {}), pcall(setfenv, 1, 2))
print(getfenv(0) == _G, getfenv() == _G, getfenv(print) == _G, pcall(getfenv, "x"))
print(pcall(setfenv, print, {}))
print(pcall(setfenv, 0, _G))
local f = function() return x end
print(setfenv(f, {x = 5}) == f, f())
print(pcall(string.char, 256), pcall(string.char, -1), string.char(65.9), pcall(string.char, "x"))
print(pcall(table.insert, {}, 1, 2, 3), pcall(table.insert, {}))
print(pcall(table.concat, {1, {}, 3}), pcall(table.concat, {1, 2}, ",", 1, 5))
print(pcall(table.setn, {}, 3), pcall(table.foreach, {}, 1))

print(type(gcinfo()), gcinfo() == math.floor(gcinfo()), gcinfo() > 0)
local seen = {}
print(table.foreach({10, 20, x = 30}, function(k, v) seen[#seen + 1] = tostring(k) .. "=" .. v end), #seen)
print(table.foreach({1, 2, 3}, function(k, v) if v == 2 then return "found", k end end))
print(table.foreachi({5, 6, nil, 8}, function(i, v) seen[#seen + 1] = i end), #seen)
print(table.foreachi({5, 6, 7}, function(i, v) if v > 5 then return v * 10 end end))
print(table.getn({1, 2, 3}), table.getn({}), pcall(table.getn, "x"))
print(pcall(table.setn, {}, 1), pcall(table.setn, 1, 1))
print(math.mod(7, 3), math.mod(-7, 3), math.mod == math.fmod)
local words = {}
for w in string.gfind("one two three", "%a+") do words[#words + 1] = w end
print(table.concat(words, ","))
local lt = setmetatable({1, 2, 3}, {__len = function() return 42 end})
print(#lt, #setmetatable({}, {__len = function() return 1 end}))
local ud = newproxy(true)
getmetatable(ud).__len = function() return 7 end
print(#ud)
print(pcall(string.char, 255, 0, 2^31), pcall(string.char, 2^32 + 65))
