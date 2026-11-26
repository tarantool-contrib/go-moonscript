module("my_module", package.seeall)
local length
length = function(x, y)
  return math.sqrt(x * x + y * y)
end
print_result = function(x, y)
  return print("Length is ", length(x, y))
end
require("my_module")
my_module.print_result(4, 5)
return print(my_module.length(6, 7))