local thing = {
  color = "red",
  name = "fast",
  width = 123
}
local thing_copy
do
  local _tbl_0 = { }
  for k, v in pairs(thing) do
    _tbl_0[k] = v
  end
  thing_copy = _tbl_0
end