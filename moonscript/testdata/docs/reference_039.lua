local x_coords = {
  4,
  5,
  6,
  7
}
local y_coords = {
  9,
  2,
  3
}
local points
do
  local _accum_0 = { }
  local _len_0 = 1
  for _index_0 = 1, #x_coords do
    local x = x_coords[_index_0]
    for _index_1 = 1, #y_coords do
      local y = y_coords[_index_1]
      _accum_0[_len_0] = {
        x,
        y
      }
      _len_0 = _len_0 + 1
    end
  end
  points = _accum_0
end