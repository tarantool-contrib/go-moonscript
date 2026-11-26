local _class_0
do
  local _class_1
  local _base_0 = {
    x = function(self)
      return _class_0
    end
  }
  _base_0.__index = _base_0
  _class_1 = setmetatable({
    __init = function() end,
    __base = _base_0,
    __name = "_class_0"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_1
  _class_0 = _class_1
end
local _base_0
do
  local _class_1
  local _parent_0 = _parent_0
  local _base_1 = { }
  _base_1.__index = _base_1
  setmetatable(_base_1, _parent_0.__base)
  _class_1 = setmetatable({
    __init = function(self, ...)
      return _class_1.__parent.__init(self, ...)
    end,
    __base = _base_1,
    __name = "_base_0",
    __parent = _parent_0
  }, {
    __index = function(cls, name)
      local val = rawget(_base_1, name)
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
      local _self_0 = setmetatable({}, _base_1)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_1.__class = _class_1
  if _parent_0.__inherited then
    _parent_0.__inherited(_parent_0, _class_1)
  end
  _base_0 = _class_1
  return _class_1
end
