-- yuin/gopher-lua#490, #553: a short-circuited call result in an "and" must be the value of the expression
local function id(...) return ... end
local r = 1
r = id(false) and id(2)
print(r)
r = 1
r = id(nil) and id(2) and id(3)
print(r)
local t = {}
t.x = 1
t.x = id(false) and id(2)
print(t.x)
r = true
r = r and (id(false) and id(true))
print(r)
r = true
r = r and (id(1) and id(false))
print(r)
r = 1
r = r and (id(nil) or id(false))
print(r)
r = 1
r = (r and id(false)) or id(nil)
print(r)
g = true
g = g and (id(false) and id(true))
print(g)
local u = {v = true}
u.v = u.v and (id(false) and id(true))
print(u.v)
local function between(val, a, b)
  local result = true
  result = result and (id(val > a) and id(val < b))
  return result
end
print(between(2, 3, 5), between(4, 3, 5), between(6, 3, 5))
local x = id(0) and (id(false) and 1)
print(x)
local y = nil
y = (id(1) and (id(nil) and 2)) or (id(false) and 3)
print(y)
