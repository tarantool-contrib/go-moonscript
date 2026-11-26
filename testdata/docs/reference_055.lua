local i = 0
while i < 10 do
  local _continue_0 = false
  repeat
    if i % 2 == 0 then
      _continue_0 = true
      break
    end
    print(i)
    _continue_0 = true
  until true
  if not _continue_0 then
    break
  end
end