-- "local f = function" doesn't see the local f in its body, "local function
-- f" does
f = "global f"
local f = function() return f end
print(f())
local function g(n) if n > 0 then return g(n - 1) end return "g" end
print(g(3))
local h = "outer h"
do
  local h = function() return h end
  print(h())
end
local k = 1
local k = function() return k end
print(k())
local fact = function(n) return fact end
print(fact(1))
