local doubled
do
  local _accum_0 = { }
  local _len_0 = 1
  local _list_0 = items
  for _index_0 = 1, #_list_0 do
    local item = _list_0[_index_0]
    _accum_0[_len_0] = item * 2
    _len_0 = _len_0 + 1
  end
  doubled = _accum_0
end