-- collectgarbage's results and the steps of a cycle (Lua 5.1)
print(collectgarbage("stop"), collectgarbage("step"), collectgarbage("restart"), collectgarbage("step"))
print(collectgarbage("collect"), collectgarbage(), select("#", collectgarbage("count")))
local kb = gcinfo() -- one reading: two may differ by the allocations between them
print(type(collectgarbage("count")), type(kb), kb == math.floor(kb))
print(collectgarbage("setpause", 150), collectgarbage("setpause", 200))
print(collectgarbage("setstepmul", 400), collectgarbage("setstepmul", 200))
print((pcall(collectgarbage, "isrunning")))
print((pcall(collectgarbage, "count", "x")))
-- a big step finishes a cycle, small ones need several
print(collectgarbage("step", 100000))
collectgarbage()
local steps = 0
repeat steps = steps + 1 until collectgarbage("step", 0)
print("several steps", steps > 1)
collectgarbage("setstepmul", 0) -- no limit: a step finishes the cycle
print(collectgarbage("step"))
collectgarbage("setstepmul", 200)
-- count drops when garbage is collected
collectgarbage()
local before = collectgarbage("count")
local t = {}
for i = 1, 20000 do t[i] = {i} end
local full = collectgarbage("count")
t = nil
collectgarbage()
print("count", full > before + 100, collectgarbage("count") < full - 100)
-- weak tables are cleared by the automatic cycles too
local w = setmetatable({[1] = {}}, {__mode = "v"})
local n = 0
while w[1] do
  local s = n .. "x" .. n
  n = n + 1
end
print("automatic", w[1])
