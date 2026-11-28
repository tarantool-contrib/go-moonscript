local Things
do
  local _class_0
  local helper
  local x
  local _base_0 = {
    y = function(self)
      return x
    end
  }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function() end,
    __base = _base_0,
    __name = "Things"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  local self = _class_0
  helper = function()
    return 1
  end
  print(helper())
  self.__class.prop = 2
  x = 5
  Things = _class_0
  return _class_0
end
