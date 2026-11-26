local Foo
do
  local _class_0
  local _base_0 = {
    meth = function(self)
      return _class_0.__parent.meth(self)
    end
  }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function(self)
      return _class_0.__parent.__init(self)
    end,
    __base = _base_0,
    __name = "Foo"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  Foo = _class_0
  return _class_0
end
