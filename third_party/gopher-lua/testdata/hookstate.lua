-- computes a string that depends on registers, varargs, multiple results,
-- metamethods, closures, coroutines and errors; hook_test.go runs it with
-- and without hooks and compares
local out = {}
local function add(...) out[#out + 1] = table.concat({...}, ",") end

local function multi() return 1, 2, 3 end
local function pass(...) return ... end
add(multi())
add(pass(multi()))
add(select("#", pass(nil, nil)), select(2, multi()))
local t = {multi(), multi()}
add(#t, t[4] or "nil")
local function va(...)
  local n = select("#", ...)
  local a, b = ...
  return n, a, b, ...
end
add(va(4, 5, 6))
local mt = {
  __index = function(t, k) return k .. "?" end,
  __add = function(a, b) return 100 end,
  __concat = function(a, b) return "cat" end,
  __call = function(self, x) return x * 2 end,
  __eq = function() return true end,
  __lt = function() return true end,
}
local o = setmetatable({}, mt)
local p = setmetatable({}, mt)
add(o.foo, o + 1, o .. "x", o(21), tostring(o == p), tostring(o < p))
local function counter()
  local c = 0
  return function() c = c + 1; return c end
end
local inc = counter()
inc(); inc()
add(inc())
local co = coroutine.wrap(function(a, b)
  local c = coroutine.yield(a + b)
  local d, e = coroutine.yield(c * 2)
  return d + e, "end"
end)
add(co(1, 2), co(10), co(3, 4))
local ok, err = pcall(function(x) error({code = x}) end, 7)
add(tostring(ok), err.code)
local ok2, a2, b2 = pcall(multi)
add(tostring(ok2), a2, b2)
local s = 0
for i = 1, 10 do
  if i % 2 == 0 then s = s + i else s = s - 1 end
end
for k, v in ipairs({5, 6, 7}) do s = s + k * v end
add(s)
local function fact(n) if n <= 1 then return 1 end return n * fact(n - 1) end
local function tfact(n, acc) if n <= 1 then return acc end return tfact(n - 1, acc * n) end
add(fact(10), tfact(10, 1))
add(string.format("%d-%s", 3, "x"), ("abc"):upper(), #"hello")
local u = {n = 0}
function u:bump(k) self.n = self.n + k; return self end
u:bump(1):bump(2)
add(u.n)
return table.concat(out, ";")
