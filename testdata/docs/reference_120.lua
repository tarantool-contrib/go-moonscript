local MY_CONSTANT = "hello"
local my_function
my_function = function()
  return print("the function")
end
local my_second_function
my_second_function = function()
  return print("another function")
end
return {
  my_function = my_function,
  my_second_function = my_second_function,
  MY_CONSTANT = MY_CONSTANT
}