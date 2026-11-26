local Counter
do
  local _class_0
  local _base_0 = { }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function(self)
      self.__class.count = self.__class.count + 1
    end,
    __base = _base_0,
    __name = "Counter"
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
  self.count = 0
  Counter = _class_0
end
Counter()
Counter()
return print(Counter.count)