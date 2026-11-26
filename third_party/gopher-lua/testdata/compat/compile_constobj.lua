-- yuin/gopher-lua#538, #310: assigning to a field of a constant
local function t(s)
  local ok, msg = pcall(assert(loadstring(s)))
  if type(msg) == "string" then msg = string.gsub(msg, "^.-:%d+: ", "") end
  print(ok, type(msg) == "string" and string.match(msg, "index a ") and (string.match(msg, "(%a+)%)") or string.match(msg, "(%a+) value")) or msg)
end
t[[local t = {}; (5).field = 1; return t]]
t[[(123).K = nil]]
t[[_=1; (123).K = nil]]
t[[local x = 5; (true).K = nil; return x]]
t[[local a, b = 1, 2; ('x').y = 3; return a, b]]
t[[local a = 1; (nil).y, a = 3, 4; return a]]
t[[local t = {}; (t).x = 1; return t.x]]
local savedmt = debug.getmetatable("")
debug.setmetatable("", {})
t[[("Lua").key = 1]]
debug.setmetatable("", savedmt)
local mt = {__newindex = function(s, k, v) print("newindex", s, k, v) end}
debug.setmetatable(0, mt)
t[[(7).k = 1]]
t[[local a = 1; (8)[a] = 2; return a]]
debug.setmetatable(0, nil)
