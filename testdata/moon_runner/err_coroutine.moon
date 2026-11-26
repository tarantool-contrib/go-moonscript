co = coroutine.create ->
  coroutine.yield 1
  error "inside coroutine"
print coroutine.resume co
ok, err = coroutine.resume co
print ok, err
print coroutine.status co
w = coroutine.wrap ->
  for i = 1, 3
    coroutine.yield i * 10
print w!, w!, w!
