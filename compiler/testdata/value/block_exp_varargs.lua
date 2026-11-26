local f
f = function(...)
  return print((function(...)
    if a then
      return ...
    else
      return 1
    end
  end)(...))
end
local g
g = function(...)
  local h
  h = function()
    return ...
  end
  return print((function()
    if a then
      return h
    else
      return 2
    end
  end)())
end
