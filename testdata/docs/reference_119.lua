local tmp = 1213
local i, k = 100, 50
local my_func
my_func = function(add)
  local tmp = tmp + add
  i = i + tmp
  k = k + tmp
end
my_func(22)
return print(i, k)