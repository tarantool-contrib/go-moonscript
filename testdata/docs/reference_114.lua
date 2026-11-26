local tuples = {
  {
    "hello",
    "world"
  },
  {
    "egg",
    "head"
  }
}
for _index_0 = 1, #tuples do
  local _des_0 = tuples[_index_0]
  local left, right
  left, right = _des_0[1], _des_0[2]
  print(left, right)
end