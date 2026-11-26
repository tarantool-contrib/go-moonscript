local t = {
  (function()
    local A
    do
      local _class_0
      local _base_0 = { }
      _base_0.__index = _base_0
      _class_0 = setmetatable({
        __init = function() end,
        __base = _base_0,
        __name = "A"
      }, {
        __index = _base_0,
        __call = function(cls, ...)
          local _self_0 = setmetatable({}, _base_0)
          cls.__init(_self_0, ...)
          return _self_0
        end
      })
      _base_0.__class = _class_0
      A = _class_0
      return _class_0
    end
  end)(),
  (function()
    do
      local _class_0
      local _parent_0 = A
      local _base_0 = { }
      _base_0.__index = _base_0
      setmetatable(_base_0, _parent_0.__base)
      _class_0 = setmetatable({
        __init = function(self, ...)
          return _class_0.__parent.__init(self, ...)
        end,
        __base = _base_0,
        __name = nil,
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
      return _class_0
    end
  end)()
}
local f
f = function(cls)
  return cls
end
f((function()
  local C
  do
    local _class_0
    local _base_0 = {
      y = 1
    }
    _base_0.__index = _base_0
    _class_0 = setmetatable({
      __init = function() end,
      __base = _base_0,
      __name = "C"
    }, {
      __index = _base_0,
      __call = function(cls, ...)
        local _self_0 = setmetatable({}, _base_0)
        cls.__init(_self_0, ...)
        return _self_0
      end
    })
    _base_0.__class = _class_0
    C = _class_0
    return _class_0
  end
end)())
for i = 1, 3 do
  local D
  do
    local _class_0
    local _base_0 = { }
    _base_0.__index = _base_0
    _class_0 = setmetatable({
      __init = function() end,
      __base = _base_0,
      __name = "D"
    }, {
      __index = _base_0,
      __call = function(cls, ...)
        local _self_0 = setmetatable({}, _base_0)
        cls.__init(_self_0, ...)
        return _self_0
      end
    })
    _base_0.__class = _class_0
    D = _class_0
  end
end
local x
do
  local _accum_0 = { }
  local _len_0 = 1
  for i = 1, 2 do
    local E
    do
      local _class_0
      local _base_0 = { }
      _base_0.__index = _base_0
      _class_0 = setmetatable({
        __init = function() end,
        __base = _base_0,
        __name = "E"
      }, {
        __index = _base_0,
        __call = function(cls, ...)
          local _self_0 = setmetatable({}, _base_0)
          cls.__init(_self_0, ...)
          return _self_0
        end
      })
      _base_0.__class = _class_0
      E = _class_0
      _accum_0[_len_0] = _class_0
    end
    _len_0 = _len_0 + 1
  end
  x = _accum_0
end
