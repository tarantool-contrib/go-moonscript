do
  local _with_0 = Person()
  _with_0.name = "Oswald"
  _with_0:add_relative(my_dad)
  _with_0:save()
  print(_with_0.name)
  return _with_0
end