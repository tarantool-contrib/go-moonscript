-- Table destructuring: in assignments, nested patterns, defaults through
-- `or`, loop headers and function results.
N = tonumber(arg and arg[1]) or 1000

points = [{x: i, y: i * 2, meta: {tag: "p#{i % 4}", w: i % 3}} for i = 1, 50]
tuples = [{i, i + 1, {i + 2, i + 3}} for i = 1, 50]

minmax = (t) ->
  lo, hi = t[1], t[1]
  for v in *t
    lo = v if v < lo
    hi = v if v > hi
  lo, hi

sx, sy, sw, sn = 0, 0, 0, 0
for _ = 1, N
  for {:x, :y, meta: {:tag, :w}} in *points
    sx += x
    sy += y
    sw += w + #tag
  for {a, b, {c, d}} in *tuples
    sn += a + b + c + d
  {first, second} = tuples
  {x: fx, y: fy} = points[1]
  {_, {_, _, {third}}} = tuples
  lo, hi = minmax {first[1], second[2], fx, fy, third}
  sn += lo + hi

print sx, sy, sw, sn
