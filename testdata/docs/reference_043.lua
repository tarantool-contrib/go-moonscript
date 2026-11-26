local numbers = {
  1,
  2,
  3,
  4
}
local sqrts
do
  local _tbl_0 = { }
  for _index_0 = 1, #numbers do
    local i = numbers[_index_0]
    _tbl_0[i] = math.sqrt(i)
  end
  sqrts = _tbl_0
end