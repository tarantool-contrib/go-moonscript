local my_object = {
  value = 1000,
  write = function(self)
    return print("the value:", self.value)
  end
}
local run_callback
run_callback = function(func)
  print("running callback...")
  return func()
end
run_callback(my_object.write)
return run_callback((function()
  local _base_0 = my_object
  local _fn_0 = _base_0.write
  return function(...)
    return _fn_0(_base_0, ...)
  end
end)())