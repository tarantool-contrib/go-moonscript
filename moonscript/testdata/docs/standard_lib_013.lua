local List
do
  local _class_0
  local _base_0 = {
    add = function(self, item)
      return print("adding to", self)
    end,
    remove = function(self, item)
      return print("removing from", self)
    end
  }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function() end,
    __base = _base_0,
    __name = "List"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  List = _class_0
end
local Encapsulation
do
  local _class_0
  local _base_0 = { }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function(self)
      self.list = List()
      return mixin_object(self, self.list, {
        "add",
        "remove"
      })
    end,
    __base = _base_0,
    __name = "Encapsulation"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  Encapsulation = _class_0
end
local e = Encapsulation()
return e.add("something")