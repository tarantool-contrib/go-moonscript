local z
do
  local _accum_0 = { }
  local _len_0 = 1
  local _list_0 = as
  for _index_0 = 1, #_list_0 do
    local a = _list_0[_index_0]
    local _list_1 = bs
    for _index_1 = 1, #_list_1 do
      local b = _list_1[_index_1]
      if a ~= b then
        _accum_0[_len_0] = {
          a,
          b
        }
        _len_0 = _len_0 + 1
      end
    end
  end
  z = _accum_0
end
local w
do
  local _accum_0 = { }
  local _len_0 = 1
  for i = 1, 10, 2 do
    _accum_0[_len_0] = i
    _len_0 = _len_0 + 1
  end
  w = _accum_0
end
