local iter = ipairs(items)
local slice
do
  local _accum_0 = { }
  local _len_0 = 1
  for i, item in iter do
    if i > 1 and i < 3 then
      _accum_0[_len_0] = item
      _len_0 = _len_0 + 1
    end
  end
  slice = _accum_0
end