local func_a
func_a = function()
  for i = 1, 10 do
    local _scrap_0 = i
  end
end
local func_b
func_b = function()
  return (function()
    local _accum_0 = { }
    local _len_0 = 1
    for i = 1, 10 do
      _accum_0[_len_0] = i
      _len_0 = _len_0 + 1
    end
    return _accum_0
  end)()
end
print(func_a())
return print(func_b())