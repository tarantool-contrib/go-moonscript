-- debug.gethook and the masks debug.sethook accepts
local function f() end
local function show(...)
  local hook, mask, count = ...
  local name = hook == f and "f" or tostring(hook)
  print(name, mask, count, select("#", ...))
end

show(debug.gethook())
debug.sethook(f, "lrc", 5)
show(debug.gethook())
debug.sethook(f, "l")
show(debug.gethook())
debug.sethook(f, "", 0)
show(debug.gethook())
debug.sethook(f, "x", 3)
show(debug.gethook())
debug.sethook()
show(debug.gethook())
debug.sethook(nil)
show(debug.gethook())

local co = coroutine.create(function() end)
debug.sethook(co, f, "c")
show(debug.gethook(co))
show(debug.gethook())
debug.sethook(co)
show(debug.gethook(co))
