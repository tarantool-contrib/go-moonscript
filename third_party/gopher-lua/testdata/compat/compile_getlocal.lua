-- yuin/gopher-lua#413, #252: debug.getlocal scopes
local function names(level)
  local t = {}
  local i = 1
  while true do
    local name, value = debug.getlocal(level + 1, i)
    if not name then break end
    if string.sub(name, 1, 1) ~= "(" or name == "(for index)" or name == "(for generator)" or name == "(for control)" then
      t[#t + 1] = name .. "=" .. (type(value) == "function" and "fn" or type(value) == "table" and "tbl" or tostring(value))
    end
    i = i + 1
  end
  return table.concat(t, " ")
end

if true then
  local not_visible = "true"
else
  local not_visible = "false"
end
local should_exist = "foo"
print(names(1))

local function f(a, b)
  local x = 1
  do
    local y = 2
    print("in do", names(1))
  end
  local z = 3
  print("after do", names(1))
  for i = 1, 2 do
    local w = i * 2
    print("num for", names(1))
  end
  print("after num for", names(1))
  for k, v in ipairs({"p"}) do
    print("gen for", names(1))
  end
  print("after gen for", names(1))
  while x < 2 do
    local q = x
    x = x + 1
    print("while", names(1))
  end
  repeat
    local r = 5
    print("repeat", names(1))
  until r == 5
  local s = names(1)
  print("s", s)
  local u, v = names(1)
  print("u", u)
  local r = names(1)
  return r
end
print("ret", f(10, 20))

local function g(...)
  local n = select("#", ...)
  print("vararg", names(1))
end
g(1, 2)

-- line hook: the locals visible on every line event
local function h()
  local a = 1
  for i = 1, 2 do
    local b = i
  end
  for k in pairs({x = 1}) do
    local c = k
  end
  local d = 4
end
local events = {}
debug.sethook(function(ev, line)
  events[#events + 1] = line .. ": " .. names(2)
end, "l")
h()
debug.sethook()
for i = 1, #events - 1 do print(events[i]) end

-- closures (CLOSE at the end of the blocks)
local function k()
  local fs = {}
  for i = 1, 2 do
    local c = i
    fs[i] = function() return c + i end
  end
  local n = 0
  while n < 2 do
    local m = n
    fs[#fs + 1] = function() return m end
    n = n + 1
  end
  repeat
    local r = 1
    fs[#fs + 1] = function() return r end
  until r == 1
  do
    local dd = 3
    fs[#fs + 1] = function() return dd end
  end
  return #fs
end
events = {}
debug.sethook(function(ev, line)
  events[#events + 1] = line .. ": " .. names(2)
end, "l")
k()
debug.sethook()
for i = 1, #events - 1 do print(events[i]) end
