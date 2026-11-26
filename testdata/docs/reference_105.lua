local counter
do
  local i = 0
  counter = function()
    i = i + 1
    return i
  end
end
print(counter())
return print(counter())