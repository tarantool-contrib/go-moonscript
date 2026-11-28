local my_function
my_function = function(name, height)
  if name == nil then
    name = "something"
  end
  if height == nil then
    height = 100
  end
  print("Hello I am", name)
  return print("My height is", height)
end