-- line events of MoonScript output (moonc of a class, comprehensions,
-- switch, multi-line calls and tables, closures, a coroutine)
local __lines = {}
debug.sethook(function(event, line)
  if debug.getinfo(2, "S").short_src == "moon_output.lua" then
    __lines[#__lines + 1] = line
  end
end, "l")
local function moon_chunk()
local add
add = function(a, b)
  return a + b
end
local Counter
do
  local _class_0
  local _base_0 = {
    inc = function(self)
      self.n = self.n + 1
      return self.n
    end
  }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function(self, n)
      if n == nil then
        n = 0
      end
      self.n = n
    end,
    __base = _base_0,
    __name = "Counter"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  Counter = _class_0
end
local c = Counter()
for i = 1, 3 do
  c:inc()
end
if c.n > 2 then
  print("big", c.n)
else
  print("small")
end
local items
do
  local _accum_0 = { }
  local _len_0 = 1
  local _list_0 = {
    1,
    2,
    3
  }
  for _index_0 = 1, #_list_0 do
    local x = _list_0[_index_0]
    _accum_0[_len_0] = x * 2
    _len_0 = _len_0 + 1
  end
  items = _accum_0
end
local t
do
  local _tbl_0 = { }
  for k, v in pairs({
    a = 1
  }) do
    _tbl_0[k] = v
  end
  t = _tbl_0
end
while c.n < 5 do
  c:inc()
end
local s
local _exp_0 = c.n
if 5 == _exp_0 then
  s = "five"
else
  s = "other"
end
local long_call = add(1, 2)
local tbl = {
  a = 1,
  b = function()
    return "b"
  end,
  {
    nested = true
  }
}
local ok, err = pcall(function()
  return error("boom")
end)
local with_do
do
  local x = 10
  with_do = x * 2
end
if not (false) then
  print("unless")
end
local co = coroutine.wrap(function()
  coroutine.yield(1)
  return 2
end)
co()
co()
local obj
do
  local _with_0 = { }
  _with_0.x = 1
  _with_0.y = _with_0.x + 1
  obj = _with_0
end
return print(s, #items, long_call, with_do, ok, obj.x)
end
moon_chunk()
debug.sethook()
print(table.concat(__lines, " "))
