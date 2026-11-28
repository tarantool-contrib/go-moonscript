local Foo
do
  local _class_0
  local _parent_0 = Bar.Baz()
  local _base_0 = {
    ["and"] = function(self)
      return 1
    end
  }
  _base_0.__index = _base_0
  setmetatable(_base_0, _parent_0.__base)
  _class_0 = setmetatable({
    __init = function(self, a, b, ...)
      if a == nil then
        a = 1
      end
      if b == nil then
        b = 2
      end
      self.a = a
      return _class_0.__parent.__init(self, b, ...)
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
  local self = _class_0
  self["or"] = 2
  if _parent_0.__inherited then
    _parent_0.__inherited(_parent_0, _class_0)
  end
  Foo = _class_0
  return _class_0
end
