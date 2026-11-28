-- yuin/gopher-lua#440, #560: multiple assignment must evaluate every expression before assigning
local a, b = 0, 0
a, b = a + 1, a + b
print(a, b)
local x, y = 2, 1
x, y = y, x
print(x, y)
local function f(u)
  local c = 1
  u, c = u:sub(1, -2), c - 1
  return u, c
end
print(f("a)"))
local k, t = 1, {}
g440, t.x = 5, k
print(g440, t.x)
local s, d = {}, 4
s.x, d = d, 7
print(s.x, d)
do
  local a,i,j,b
  a = {'a', 'b'}; i=1; j=2; b=a
  i, a[i], a, j, a[j], a[i+j] = j, i, i, b, j, i
  print(i == 2 and b[1] == 1 and a == 1 and j == b and b[2] == 2 and b[3] == 1)
end
do
  local t = {}
  local t2 = {}
  t, t.x = t2, 1
  print(t == t2, t2.x, rawget(t2, 'x'))
end
do
  local t = {x = 1, y = 2}
  t.x, t.y = t.y, t.x
  print(t.x, t.y)
  local a = {1, 2}
  a[1], a[2] = a[2], a[1]
  print(a[1], a[2])
  local i = 1
  local u = {}
  i, u[i] = i + 1, 20
  print(i, u[1], u[2])
  u[i], i = 30, i + 1
  print(i, u[2], u[3])
end
do
  local a, b = 1, 2
  local function swap() a, b = b, a end
  swap()
  print(a, b)
  gx, gy = 1, 2
  gx, gy = gy, gx
  print(gx, gy)
  local t = {}
  local p = 3
  p, gx, t.k = gx, p, p
  print(p, gx, t.k)
  gx, gy = nil, nil
end
do
  local function f(...)
    local a, b, c = 1, 2, 3
    a, b, c = c, ...
    return a, b, c
  end
  print(f(7, 8))
  print(f(7))
  local function g() return 10, 20 end
  local a, b, c = 1, 2, 3
  a, b, c = b, g()
  print(a, b, c)
  a, b = b, a, g()
  print(a, b)
  local t = {}
  t.x, a = a or 9, 5
  print(t.x, a)
  a, t.y = 6, a and a + 1
  print(a, t.y)
  a, b = (g())
  print(a, b)
end
