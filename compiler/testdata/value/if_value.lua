print((function()
  if a then
    return b
  else
    return c
  end
end)())
f((function()
  if not (x) then
    return 1
  end
end)())
local g = {
  (function()
    if a then
      return 1
    else
      return 2
    end
  end)()
}
