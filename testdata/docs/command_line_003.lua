local my_number = 1234
local some_function
some_function = function()
  if math.random() < 0.01 then
    return my_nmuber + 10
  end
end
return some_function()