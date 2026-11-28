-- List and table comprehensions: filters, nested loops, slices, numeric
-- ranges and table comprehensions building maps.
N = tonumber(arg and arg[1]) or 100

data = [(i * 7919) % 1000 for i = 1, 1000]

checksum = 0
for _ = 1, N
  evens = [x for x in *data when x % 2 == 0]
  squares = [x * x for x in *evens]
  head = [x for x in *squares[1, 100]]
  pairs_ = [{a, b} for a in *head[1, 20] for b in *data[1, 10] when (a + b) % 3 == 0]
  map = {tostring(x), x for x in *data[1, 200]}
  inv = {v, k for k, v in pairs {a: 1, b: 2, c: 3}}
  odd_idx = [i for i = 1, #data, 2]
  sums = [p[1] + p[2] for p in *pairs_]
  checksum += #evens + #squares + #pairs_ + #odd_idx + (map["919"] or 0)
  checksum += inv[2] == "b" and 1 or 0
  for s in *sums
    checksum += s % 7

print checksum
