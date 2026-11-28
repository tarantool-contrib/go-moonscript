local slice
do
  local _accum_0 = { }
  local _len_0 = 1
  local _list_0 = items
  local _max_0 = 5
  for _index_0 = 1, _max_0 < 0 and #_list_0 + _max_0 or _max_0 do
    local item = _list_0[_index_0]
    _accum_0[_len_0] = item
    _len_0 = _len_0 + 1
  end
  slice = _accum_0
end