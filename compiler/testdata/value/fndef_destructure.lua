local f
f = function(_arg_0)
  local a, b
  a, b = _arg_0.a, _arg_0.b
  return a + b
end
local g
g = function(x, _arg_0, y)
  local a, b
  a, b = _arg_0[1], _arg_0[2]
  if y == nil then
    y = a
  end
  return x + y
end
local h
h = function(self, _arg_0, b, c, d, ...)
  if _arg_0 == nil then
    _arg_0 = { }
  end
  local a
  a = _arg_0.a
  self.b = b
  if c == nil then
    c = 1
  end
  self.__class.c = c
  if d == nil then
    d = 2
  end
  return a
end
local k
k = function(_arg_0)
  local b, c, d
  b, c, d = _arg_0.a[1], _arg_0.a[2], _arg_0.d
  return b
end
local m
m = function(self, x, _arg_0)
  local z
  self.y, z = _arg_0.y, _arg_0.z
  return z
end
