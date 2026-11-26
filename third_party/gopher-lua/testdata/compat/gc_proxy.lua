-- newproxy and __gc for userdata (Lua 5.1)
print(type(newproxy()), getmetatable(newproxy()), getmetatable(newproxy(false)))
local u = newproxy(true)
local mt = getmetatable(u)
print(type(mt), getmetatable(newproxy(u)) == mt)
local function bad(...)
  local ok, err = pcall(newproxy, ...)
  return ok, (string.match(err, "%(boolean or proxy expected%)"))
end
print(bad({}))
print(bad(1))
print(bad(newproxy(false)))
print(bad(io.stdout))

-- __gc runs for unreachable proxies, newest first, with the proxy
local log = {}
local ids = setmetatable({}, {__mode = "k"})
mt.__gc = function(o) log[#log + 1] = ids[o] end
for i = 1, 5 do ids[newproxy(u)] = i end
collectgarbage()
print("order", table.concat(log, " "))

-- __gc set on the metatable after the proxies were made
local v = newproxy(true)
local n = 0
for i = 1, 3 do newproxy(v) end
getmetatable(v).__gc = function() n = n + 1 end
collectgarbage()
print("late __gc", n)

-- the proxies keep each other alive in a cycle, and are finalized together
local fin = 0
do
  local c1 = newproxy(true)
  local cm = getmetatable(c1)
  local c2 = newproxy(c1)
  cm.peers = {c1, c2}
  cm.self = cm
  cm.__gc = function(o) fin = fin + 1; assert(getmetatable(o).peers[2]) end
end
collectgarbage()
print("cycle", fin)

-- weak values that are being finalized are cleared; weak keys stay until
-- the next cycle
local wk = setmetatable({}, {__mode = "k"})
local wv = setmetatable({}, {__mode = "v"})
local p = newproxy(true)
local seen
getmetatable(p).__gc = function(o) seen = {wk[o], wv[1]} end
wk[p] = "key"
wv[1] = p
p = nil
collectgarbage()
print("in __gc", seen[1], seen[2])
print("after", next(wk) ~= nil, wv[1])
collectgarbage()
print("next cycle", next(wk))

-- resurrection: the finalized object can be kept, its __gc does not run again
local keepit
local q = newproxy(true)
local qn = 0
getmetatable(q).__gc = function(o) qn = qn + 1; keepit = o end
q = nil
collectgarbage()
collectgarbage()
print("resurrected", type(keepit), qn)
keepit = nil
collectgarbage()
print("not again", qn)

-- an error in __gc propagates from the collection
local e = newproxy(true)
getmetatable(e).__gc = function() error("in gc") end
e = nil
print(pcall(collectgarbage))
print(pcall(collectgarbage))

-- a reachable proxy is not finalized
local live = newproxy(true)
local ln = 0
getmetatable(live).__gc = function() ln = ln + 1 end
local holder = {live}
live = nil
collectgarbage()
print("reachable", ln)
holder = nil
collectgarbage()
print("unreachable", ln)

-- proxies on a coroutine's stack are reachable while it is
local co = coroutine.create(function()
  local x = newproxy(true)
  getmetatable(x).__gc = function() print("co proxy finalized") end
  coroutine.yield()
  return type(x)
end)
coroutine.resume(co)
collectgarbage()
print("co alive", coroutine.resume(co))
co = nil
collectgarbage()

print("end")
