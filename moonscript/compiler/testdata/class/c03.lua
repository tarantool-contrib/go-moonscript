local Bar
do
  local _class_0
  local _parent_0 = Foo
  local _base_0 = {
    hi = function(self, x)
      return _class_0.__parent.__base.hi(self, x)
    end,
    other = function(self)
      return _class_0.__parent.hello(self, 1, 2)
    end,
    stub = function(self)
      local _base_1 = _class_0.__parent
      local _fn_0 = _base_1.hello
      return function(...)
        return _fn_0(self, ...)
      end
    end,
    [k] = function(self)
      return _class_0.__parent.__base[k](self)
    end,
    ["str"] = function(self)
      return _class_0.__parent.__base["str"](self, 1)
    end
  }
  _base_0.__index = _base_0
  setmetatable(_base_0, _parent_0.__base)
  _class_0 = setmetatable({
    __init = function(self, a)
      _class_0.__parent.__init(self, a)
      return _class_0.__parent.thing(self)
    end,
    __base = _base_0,
    __name = "Bar",
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
  self.cm = function(self)
    return _class_0.__parent.cm(self)
  end
  if _parent_0.__inherited then
    _parent_0.__inherited(_parent_0, _class_0)
  end
  Bar = _class_0
  return _class_0
end
