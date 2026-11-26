local MoreThings
do
  local _class_0
  local secret, log
  local _base_0 = {
    some_method = function(self)
      return log("hello world: " .. secret)
    end
  }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function() end,
    __base = _base_0,
    __name = "MoreThings"
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
  secret = 123
  log = function(msg)
    return print("LOG:", msg)
  end
  MoreThings = _class_0
  return _class_0
end