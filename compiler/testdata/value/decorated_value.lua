local f
f = function()
  if y then
    return print(x)
  end
end
local g
g = function()
  local _list_0 = t
  for _index_0 = 1, #_list_0 do
    local x = _list_0[_index_0]
    local _scrap_0 = x
  end
end
local h
h = function()
  local _accum_0 = { }
  local _len_0 = 1
  local _list_0 = t
  for _index_0 = 1, #_list_0 do
    local x = _list_0[_index_0]
    _accum_0[_len_0] = x
    _len_0 = _len_0 + 1
  end
  return _accum_0
end
