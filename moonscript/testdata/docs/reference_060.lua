local is_tall
is_tall = function(name)
  if name == "Rob" then
    return true
  else
    return false
  end
end
local message
if is_tall("Rob") then
  message = "I am very tall"
else
  message = "I am not so tall"
end
return print(message)