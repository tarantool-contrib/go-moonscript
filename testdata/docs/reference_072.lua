local Person
do
  local _class_0
  local _base_0 = {
    clothes = { },
    give_item = function(self, name)
      return table.insert(self.clothes, name)
    end
  }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function() end,
    __base = _base_0,
    __name = "Person"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  Person = _class_0
end
local a = Person()
local b = Person()
a:give_item("pants")
b:give_item("shirt")
local _list_0 = a.clothes
for _index_0 = 1, #_list_0 do
  local item = _list_0[_index_0]
  print(item)
end