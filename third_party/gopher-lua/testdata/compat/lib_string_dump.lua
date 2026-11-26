-- string.dump and loading its chunks back: the functions behave the same,
-- their upvalues are fresh (nil), detached from the dumped closure's
local function check(f, ...)
  local s = string.dump(f)
  print(type(s), s:sub(1, 4) == "\27Lua", s:byte(5) ~= nil)
  local g = assert(loadstring(s))
  local r = {pcall(g, ...)}
  for i = 2, #r do
    if type(r[i]) == "string" then r[i] = string.format("%q", r[i]) end
  end
  print(unpack(r))
  return g, s
end

check(function() return 1, "two", 3.5, nil, true, false, -0.25, 2^53 end)
check(function(...) return select("#", ...), ... end, "a", nil, "c")
check(function(a, b) return a .. "\0" .. b, #(a .. "\0" .. b) end, "x", "y")
check(function(n)
  local t = {}
  for i = 1, n do t[#t + 1] = i * i end
  local s = 0
  for _, v in ipairs(t) do s = s + v end
  while s > 100 do s = s - 100 end
  repeat s = s + 1 until s % 7 == 0
  return s, #t, table.concat(t, ",")
end, 10)
check(function(x)
  local function fact(n) if n <= 1 then return 1 end return n * fact(n - 1) end
  local add = function(y) return function(z) return x + y + z end end
  return fact(x), add(10)(100)
end, 5)

-- upvalues: fresh and nil in the loaded function, shared by its closures
local counter = 0
local function inc(n) counter = counter + (n or 1); return counter end
local g = assert(loadstring(string.dump(inc)))
local ok, err = pcall(g, 5)
print(ok, (string.find(err, "nil", 1, true)) ~= nil)
print(inc(), counter)
print(debug.setupvalue(g, 1, 40))
print(g(2), g(), counter)
local function mk()
  local v = 0
  return function() v = v + 1; return v end
end
local h = assert(loadstring(string.dump(mk)))
local c1, c2 = h(), h()
print(c1(), c1(), c2(), c1())

-- a dumped chunk keeps its source name for errors and debug.getinfo
local f = assert(loadstring("local a = ...\nreturn a.b", "=named"))
local g2 = assert(loadstring(string.dump(f)))
print(pcall(g2, {b = 7}))
print((select(2, pcall(g2, nil))):find("named:2:", 1, true) ~= nil)
print(debug.getinfo(g2, "S").source == debug.getinfo(f, "S").source)

-- the environment is the loader's globals, as for a text chunk
local env = setfenv(assert(loadstring(string.dump(function() return X end))), {X = "env"})
print(env())
X = "global"
print(assert(loadstring(string.dump(function() return X end)))())

-- dumping a function loaded from a dump
print(assert(loadstring(string.dump(assert(loadstring(string.dump(function(a) return a * 2 end))))))(21))

-- errors (the position of a luaL_error called from pcall is the errors
-- area's: removed)
local function strip(ok, msg) return ok, (string.gsub(msg, "^[^:]*:%d+: ", "")) end
print(strip(pcall(string.dump, print)))
print(strip(pcall(string.dump, string.dump)))
print(strip(pcall(string.dump, coroutine.wrap(function() end))))
print(select("#", pcall(string.dump, {})), (pcall(string.dump, {})))
print(loadstring("\27Lua"))
print(loadstring("\27"))
print(loadstring("\27Lua\82\0\1\4\8\4\8\0" .. string.rep("\0", 40)))
print(loadstring("\27Lua\0\0\0\0\0\0\0\0", "=bin"))
print(loadstring("\27Lu", "@file.luac"))
