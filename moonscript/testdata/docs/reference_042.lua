local no_color
do
  local _tbl_0 = { }
  for k, v in pairs(thing) do
    if k ~= "color" then
      _tbl_0[k] = v
    end
  end
  no_color = _tbl_0
end