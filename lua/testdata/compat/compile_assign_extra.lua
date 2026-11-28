-- go-moonscript patch: an assignment to a local evaluates all its values
-- before it assigns the local (i = 5, f(i) passes f the old i), and a
-- vararg value for a local leaves the registers above it alone (they were
-- cleared: a Go panic when read). Found by FuzzCompileAST.

local i = 1
i = 5, print("old i", i)
print("new i", i)

local a, b = 1, 2
a = 3, b, print("a b", a, b)
print("a b", a, b)

local t = {}
local k = 1
k, t[k] = 2, k, print("k", k)
print("k t[1] t[2]", k, t[1], t[2])

local function f(...)
  local x, y = 1, 2
  x = (...)
  local u, v = 1, 2
  v, u = 3, (...)
  local p, q = 1, 2
  p = ..., ...
  return x, y, u, v, p, q
end
print(f(7, 8))
print(f())

local j = 0
local n = 0
repeat
  local m = j
  j = ..., ...
  n = n + 1
until m == nil or n > 3
print("j n", j, n)
