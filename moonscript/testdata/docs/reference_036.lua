local items = {
  1,
  2,
  3,
  4
}
local doubled
do
  local _accum_0 = { }
  local _len_0 = 1
  for i, item in ipairs(items) do
    _accum_0[_len_0] = item * 2
    _len_0 = _len_0 + 1
  end
  doubled = _accum_0
end