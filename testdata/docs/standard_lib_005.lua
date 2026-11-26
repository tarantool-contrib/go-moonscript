local copy
copy = function(arg)
  local _tbl_0 = { }
  for k, v in pairs(self) do
    _tbl_0[k] = v
  end
  return _tbl_0
end