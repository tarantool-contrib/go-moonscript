-- my_module.moon
module "my_module", package.seeall
export print_result

length = (x, y) -> math.sqrt x*x + y*y

print_result = (x, y) ->
  print "Length is ", length x, y

-- main.moon
require "my_module"

my_module.print_result 4, 5 -- prints the result

print my_module.length 6, 7 -- errors, `length` not visible
