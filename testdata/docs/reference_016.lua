local some_args
some_args = function(x, y)
  if x == nil then
    x = 100
  end
  if y == nil then
    y = x + 1000
  end
  return print(x + y)
end