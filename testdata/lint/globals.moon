print undefined_thing
x = some_global.field
some_function 1, 2
y = math.floor 2.5
require("mod").call!

export exported_name
exported_name = 5
print exported_name

z = "#{interpolated} string"

tbl = {
  key: value_global
  [computed_key]: 1
  shorthand: true
}

with obj_global
  .method!
  print \other!

class Thing extends BaseGlobal
  new: (@name, opts = default_opts) =>
    super!
  method: => @name .. missing_in_method

fn = (a = default_arg_global) -> a + other_global

print x, y, z, tbl, Thing, fn, nil, true, false, _G, _VERSION
