local doubled_evens
do
  local _accum_0 = { }
  local _len_0 = 1
  for i = 1, 20 do
    if i % 2 == 0 then
      _accum_0[_len_0] = i * 2
    else
      _accum_0[_len_0] = i
    end
    _len_0 = _len_0 + 1
  end
  doubled_evens = _accum_0
end