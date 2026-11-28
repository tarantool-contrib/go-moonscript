local slice
do
  local _accum_0 = { }
  local _len_0 = 1
  local _list_0 = items
  for _index_0 = 2, #_list_0 do
    local item = _list_0[_index_0]
    _accum_0[_len_0] = item
    _len_0 = _len_0 + 1
  end
  slice = _accum_0
end