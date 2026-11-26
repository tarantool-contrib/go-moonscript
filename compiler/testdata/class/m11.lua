local a
do
  local X
  do
    local _class_0
    local _base_0 = { }
    _base_0.__index = _base_0
    _class_0 = setmetatable({
      __init = function() end,
      __base = _base_0,
      __name = "X"
    }, {
      __index = _base_0,
      __call = function(cls, ...)
        local _self_0 = setmetatable({}, _base_0)
        cls.__init(_self_0, ...)
        return _self_0
      end
    })
    _base_0.__class = _class_0
    X = _class_0
    a = _class_0
  end
end
local b
if c then
  local Y
  do
    local _class_0
    local _base_0 = { }
    _base_0.__index = _base_0
    _class_0 = setmetatable({
      __init = function() end,
      __base = _base_0,
      __name = "Y"
    }, {
      __index = _base_0,
      __call = function(cls, ...)
        local _self_0 = setmetatable({}, _base_0)
        cls.__init(_self_0, ...)
        return _self_0
      end
    })
    _base_0.__class = _class_0
    Y = _class_0
    b = _class_0
  end
end
local z
local _exp_0 = q
if 1 == _exp_0 then
  local Z
  do
    local _class_0
    local _base_0 = { }
    _base_0.__index = _base_0
    _class_0 = setmetatable({
      __init = function() end,
      __base = _base_0,
      __name = "Z"
    }, {
      __index = _base_0,
      __call = function(cls, ...)
        local _self_0 = setmetatable({}, _base_0)
        cls.__init(_self_0, ...)
        return _self_0
      end
    })
    _base_0.__class = _class_0
    Z = _class_0
    z = _class_0
  end
end
