local f
f = function()
  return 1
end
local g
g = function(a, b)
  return a + b
end
local h
h = function(self, a, b)
  if b == nil then
    b = 10
  end
  return self.x + a + b
end
local k
k = function()
  print(1)
  if a then
    return b
  else
    return c
  end
end
local m
m = function() end
local nn
nn = function() end
