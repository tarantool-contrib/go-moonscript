local my_numbers = {
  1,
  2,
  3,
  4,
  5,
  6
}
local odds
do
  local _accum_0 = { }
  local _len_0 = 1
  for _index_0 = 1, #my_numbers do
    local _continue_0 = false
    repeat
      local x = my_numbers[_index_0]
      if x % 2 == 1 then
        _continue_0 = true
        break
      end
      local _value_0 = x
      _accum_0[_len_0] = _value_0
      _len_0 = _len_0 + 1
      _continue_0 = true
    until true
    if not _continue_0 then
      break
    end
  end
  odds = _accum_0
end