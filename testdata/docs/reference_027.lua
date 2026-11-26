if func(1, 2, 3, "hello", "world") then
  print("hello")
  print("I am inside if")
end
if func(1, 2, 3, "hello", "world") then
  print("hello")
  return print("I am inside if")
end