-- math.random checks its interval and its number of arguments
local function try(...)
  local args = {...}
  local n = select("#", ...)
  local ok, err = pcall(function() return math.random(unpack(args, 1, n)) end)
  -- (gopher-lua does not quote the function name in "bad argument" messages)
  print(ok, ok and type(err) or (string.gsub(err, "'random'", "random")))
end
try()
try(1)
try(5)
try(0)
try(-3)
try(3, 3)
try(-5, 5)
try(2, 1)
try(1, 2, 3)
try(1, 2, 3, 4)
for _ = 1, 1000 do
  local a, b, c, d = math.random(), math.random(3), math.random(-2, 2), math.random(7, 7)
  assert(a >= 0 and a < 1)
  assert(b >= 1 and b <= 3 and b == math.floor(b))
  assert(c >= -2 and c <= 2 and c == math.floor(c))
  assert(d == 7)
end
print(math.random(-2^40, 2^40) % 1 == 0)
