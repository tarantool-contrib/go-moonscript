local draw
draw = function(_arg_0)
  local label, x, y
  label, x, y = _arg_0[1], _arg_0.pos[1], _arg_0.pos[2]
  return print(label, x, y)
end
return draw({
  "origin",
  pos = {
    0,
    0
  }
})