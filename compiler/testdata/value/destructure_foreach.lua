local _list_0 = list
for _index_0 = 1, #_list_0 do
  local _des_0 = _list_0[_index_0]
  local a, b
  a, b = _des_0[1], _des_0[2]
  print(a, b)
end
for _des_0 in pairs(t) do
  local x
  x = _des_0.x
  print(x)
end
