local Foo
do
  local _class_0
  local _parent_0 = Bar
  local _base_0 = {
    m = function(self)
      _class_0.__parent.m(self)
      local _scrap_0 = _class_0.__parent.__base.m(self).x
      _class_0.__parent.n(self):o()
      return (function()
        return _class_0.__parent.__base.m(self)
      end)()
    end
  }
  _base_0.__index = _base_0
  setmetatable(_base_0, _parent_0.__base)
  _class_0 = setmetatable({
    __init = function(self, ...)
      return _class_0.__parent.__init(self, ...)
    end,
    __base = _base_0,
    __name = "Foo",
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
  if _parent_0.__inherited then
    _parent_0.__inherited(_parent_0, _class_0)
  end
  Foo = _class_0
  return _class_0
end
