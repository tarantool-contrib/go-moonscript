local C
do
  local _class_0
  local _base_0 = {
    m = function(self, x, _arg_0, z)
      local y
      y = _arg_0[1]
      return x + y + z
    end
  }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function(self, _arg_0)
      if _arg_0 == nil then
        _arg_0 = { }
      end
      local a, b
      a, b = _arg_0.a, _arg_0.b
      self.a = a
    end,
    __base = _base_0,
    __name = "C"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  C = _class_0
  return _class_0
end
