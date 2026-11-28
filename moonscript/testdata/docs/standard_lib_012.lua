local First
do
  local _class_0
  local _base_0 = {
    show_var = function(self)
      return print("var is:", self.var)
    end
  }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function(self, var)
      self.var = var
    end,
    __base = _base_0,
    __name = "First"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  First = _class_0
end
local Second
do
  local _class_0
  local _base_0 = { }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function(self)
      return mixin(self, First, "hi")
    end,
    __base = _base_0,
    __name = "Second"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  Second = _class_0
end
local a = Second()
return a:show_var()