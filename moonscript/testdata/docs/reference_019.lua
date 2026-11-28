local render
render = function(name, _arg_0, ...)
  if _arg_0 == nil then
    _arg_0 = {
      width = 100,
      height = 50
    }
  end
  local width, height
  width, height = _arg_0.width, _arg_0.height
  return print(name, width, height, ...)
end