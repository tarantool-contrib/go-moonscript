-- debug.getinfo: Lua 5.1's fields (short_src, what, activelines, func, nups,
-- linedefined/lastlinedefined), options, threads and tail call levels
local function show(t)
  if t == nil then return "nil" end
  local keys = {}
  for k in pairs(t) do keys[#keys+1] = k end
  table.sort(keys)
  local r = {}
  for _, k in ipairs(keys) do
    local v = t[k]
    -- (name and namewhat are the errors area's; gopher-lua's chunk names
    -- are display names: source is "@<name>" on Lua 5.1, short_src the same)
    if k == "name" or k == "namewhat" then v = nil end
    if k == "source" and type(v) == "string" then v = v:gsub("^@", "") end
    if type(v) == "table" then
      local l = {} for line in pairs(v) do l[#l+1] = line end table.sort(l) v = "{" .. table.concat(l, ",") .. "}"
    elseif type(v) == "function" then v = (v == print and "print") or "fn" end
    if v ~= nil then r[#r+1] = k .. "=" .. tostring(v) end
  end
  return table.concat(r, " ")
end
local function f(a, b)
  local x = a + b
  return x
end
print(show(debug.getinfo(f)))
print(show(debug.getinfo(print)))
print(show(debug.getinfo(f, "L")))
print(show(debug.getinfo(print, "SL")))
print(show(debug.getinfo(1)))
print(show(debug.getinfo(1, "Su")))
print(show(debug.getinfo(100)))
local up1, up2 = 1, 2
local function g() return up1 + up2 end
print(debug.getinfo(g, "u").nups, debug.getinfo(g, "f").func == g)
local function try(...)
  local ok, err = pcall(...)
  return ok, (string.gsub(err, "^.-bad argument", "bad argument"):gsub("to '%?'", "to ?"):gsub("to %(anonymous%)", "to ?"))
end
print(try(debug.getinfo, {}))
print(try(debug.getinfo, f, "X"))
print(try(debug.getinfo, f, ">S"))
print(try(debug.getinfo))
local function tail() return debug.getinfo(2, "Sl") end
local function caller() return tail() end
print(show(caller()))
local co = coroutine.create(function(x) coroutine.yield(x) end)
coroutine.resume(co, 1)
print(show(debug.getinfo(co, 1, "Sl")))
print(show(debug.getinfo(co, 0, "S")))
print(show(debug.getinfo(co, 5)))
print(show(debug.getinfo(loadstring("return 1", "=name"), "S")))
print(show(debug.getinfo(loadstring("\n\nlocal x = 1\nreturn x", "@file.lua"), "SL")))
print(show(debug.getinfo("1", "l")))
print(show(debug.getinfo(string.dump, "Su")), debug.getinfo(string.dump).func == string.dump)
local function deep(n) if n == 0 then return debug.getinfo(1, "S"), debug.getinfo(2, "S") end return (deep(n - 1)) end
print(show(deep(3)))
print(show(select(2, deep(0))))
local co2 = coroutine.create(function() return debug.getinfo(1, "S"), debug.getinfo(2, "S") end)
local ok, a, b = coroutine.resume(co2)
print(ok, show(a), show(b))
print(show(debug.getinfo(co2, 0)), coroutine.status(co2))
print(show(debug.getinfo(coroutine.create(function() end), 0)))
local co3
co3 = coroutine.create(function()
  local x = 5
  coroutine.yield()
end)
coroutine.resume(co3)
print(debug.getlocal(co3, 1, 1), debug.setlocal(co3, 1, 1, 6), debug.getlocal(co3, 1, 1))
print(debug.getlocal(co3, 0, 1), try(debug.getlocal, co3, 5, 1))
