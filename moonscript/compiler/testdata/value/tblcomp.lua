local x
do
  local _tbl_0 = { }
  for k, v in pairs(t) do
    _tbl_0[k] = v
  end
  x = _tbl_0
end
local y
do
  local _tbl_0 = { }
  for t in pairs(list) do
    local _key_0, _val_0 = unpack(t)
    _tbl_0[_key_0] = _val_0
  end
  y = _tbl_0
end
local z
do
  local _tbl_0 = { }
  for k, v in pairs(t) do
    if v > 1 then
      _tbl_0[k] = v * 2
    end
  end
  z = _tbl_0
end
local w
do
  local _tbl_0 = { }
  for i = 1, 10 do
    _tbl_0[i] = i
  end
  w = _tbl_0
end
