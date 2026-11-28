f = (...) -> print if a then ... else 1
g = (...) ->
  h = -> ...
  print if a then h else 2
