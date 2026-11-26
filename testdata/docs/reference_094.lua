local first, second
first = function()
  return second()
end
second = function()
  return first()
end