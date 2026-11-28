local evens
do
  local _accum_0 = { }
  local _len_0 = 1
  for i = 1, 100 do
    if i % 2 == 0 then
      _accum_0[_len_0] = i
      _len_0 = _len_0 + 1
    end
  end
  evens = _accum_0
end