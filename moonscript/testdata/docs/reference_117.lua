local i = 100
local my_func
my_func = function()
  i = 10
  while i > 0 do
    print(i)
    i = i - 1
  end
end
my_func()
return print(i)