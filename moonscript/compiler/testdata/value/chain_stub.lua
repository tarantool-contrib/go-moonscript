local f
do
  local _base_0 = a
  local _fn_0 = _base_0.b
  f = function(...)
    return _fn_0(_base_0, ...)
  end
end
local g
do
  local _base_0 = a.b.c
  local _fn_0 = _base_0.d
  g = function(...)
    return _fn_0(_base_0, ...)
  end
end
local h = ((function()
  local _base_0 = x
  local _fn_0 = _base_0.y
  return function(...)
    return _fn_0(_base_0, ...)
  end
end)())
return print((function()
  local _base_0 = obj.method
  local _fn_0 = _base_0.call
  return function(...)
    return _fn_0(_base_0, ...)
  end
end)())
