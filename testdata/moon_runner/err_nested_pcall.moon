ok, err = pcall -> error "caught"
print ok, err
ok, err = pcall -> nil + 1
print ok, (err\match "^[^:]+:%d+:")
inner = ->
  error "uncaught after pcall"
outer = -> inner!
outer!
