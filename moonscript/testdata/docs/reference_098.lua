local my_module = {
  state = 100,
  add = function(self, value)
    return self.state + value
  end
}
local add
do
  local _base_0 = my_module
  local _fn_0 = _base_0.add
  add = function(...)
    return _fn_0(_base_0, ...)
  end
end
return print(add(22))