-- closures keep sharing locals with their frames after errors are caught
-- (gopher-lua v1.1.2 detached them: see third_party/gopher-lua)
count = 0
inc = -> count += 1
pcall -> error "caught"
inc!
inc!
print "count", count

make_counter = ->
  n = 0
  get = -> n
  bump = -> n += 1
  pcall -> nil + 1
  bump!
  get!, n

print "counter", make_counter!

saved = nil
pcall ->
  secret = "kept"
  saved = -> secret
  error "after capture"
print "captured", saved!

co = coroutine.create ->
  v = "co local"
  saved = -> v
  error "co failure"
print coroutine.resume co
print "captured in coroutine", saved!
