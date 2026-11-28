-- a CPU-bound script under moon -c (every line runs the coverage hook)
fib = (n) -> if n < 2 then n else fib(n - 1) + fib(n - 2)
total = 0
for i = 1, 2000
  total += i % 7
print fib(15), total
co = coroutine.create ->
  for i = 1, 3
    coroutine.yield i
for i = 1, 3
  print coroutine.resume co
