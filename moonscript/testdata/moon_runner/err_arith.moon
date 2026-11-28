total = 0
values = {1, 2, "three", nil}

add_all = ->
  for i = 1, 4
    total = total + values[i]
  total

add_all!
