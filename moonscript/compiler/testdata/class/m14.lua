local Base
Base = require("base").Base
local Sub
do
  local _class_0
  local _parent_0 = Base
  local _base_0 = { }
  _base_0.__index = _base_0
  setmetatable(_base_0, _parent_0.__base)
  _class_0 = setmetatable({
    __init = function(self, ...)
      _class_0.__parent.__init(self, ...)
      do
        local _accum_0 = { }
        local _len_0 = 1
        for i = 1, 2 do
          _accum_0[_len_0] = _class_0.__parent.each(self)
          _len_0 = _len_0 + 1
        end
        self.x = _accum_0
      end
    end,
    __base = _base_0,
    __name = "Sub",
    __parent = _parent_0
  }, {
    __index = function(cls, name)
      local val = rawget(_base_0, name)
      if val == nil then
        local parent = rawget(cls, "__parent")
        if parent then
          return parent[name]
        end
      else
        return val
      end
    end,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  local self = _class_0
  self.__inherited = function(self, child)
    return _class_0.__parent.__inherited(self, child)
  end
  if _parent_0.__inherited then
    _parent_0.__inherited(_parent_0, _class_0)
  end
  Sub = _class_0
  return _class_0
end
