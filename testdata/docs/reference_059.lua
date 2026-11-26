local have_coins = false
return print((function()
  if have_coins then
    return "Got coins"
  else
    return "No coins"
  end
end)())