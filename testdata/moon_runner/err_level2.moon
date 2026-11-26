check = (v) ->
  error "bad value: #{v}", 2 unless type(v) == "number"
  v

caller = ->
  check 1
  check "x"

caller!
