is_tall = (name) ->
  if name == "Rob"
    true
  else
    false

message = if is_tall "Rob"
  "I am very tall"
else
  "I am not so tall"

print message -- prints: I am very tall
