local A
do
  local _class_0
  local _parent_0 = B
  local _base_0 = {
    m = function(self)
      local f
      do
        local _base_1 = _class_0.__parent
        local _fn_0 = _base_1.m
        f = function(...)
          return _fn_0(self, ...)
        end
      end
      local g
      do
        local _base_1 = self
        local _fn_0 = _base_1.x
        g = function(...)
          return _fn_0(_base_1, ...)
        end
      end
    end
  }
  _base_0.__index = _base_0
  setmetatable(_base_0, _parent_0.__base)
  _class_0 = setmetatable({
    __init = function(self, ...)
      return _class_0.__parent.__init(self, ...)
    end,
    __base = _base_0,
    __name = "A",
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
  A = _class_0
end
do
  local _with_0 = obj
  local h
  do
    local _base_0 = _with_0
    local _fn_0 = _base_0.method
    h = function(...)
      return _fn_0(_base_0, ...)
    end
  end
  do
    local _base_0 = _with_0
    local _fn_0 = _base_0.other
    _with_0.cb = function(...)
      return _fn_0(_base_0, ...)
    end
  end
  return _with_0
end
