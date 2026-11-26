local create_person
create_person = function(name, relatives)
  do
    local _with_0 = Person()
    _with_0.name = name
    for _index_0 = 1, #relatives do
      local relative = relatives[_index_0]
      _with_0:add_relative(relative)
    end
    return _with_0
  end
end
local me = create_person("Leaf", {
  dad,
  mother,
  sister
})