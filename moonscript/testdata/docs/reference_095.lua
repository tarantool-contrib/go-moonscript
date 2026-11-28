local first, second, data
first = function()
  print(data)
  return second()
end
second = function()
  return first()
end
data = { }