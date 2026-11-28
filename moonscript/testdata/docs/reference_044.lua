local tuples = {
  {
    "hello",
    "world"
  },
  {
    "foo",
    "bar"
  }
}
local tbl
do
  local _tbl_0 = { }
  for _index_0 = 1, #tuples do
    local tuple = tuples[_index_0]
    local _key_0, _val_0 = unpack(tuple)
    _tbl_0[_key_0] = _val_0
  end
  tbl = _tbl_0
end