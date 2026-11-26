local numbers = {
  4,
  3,
  5,
  6,
  7,
  2,
  3
}
local sum = fold(numbers, function(a, b)
  return a + b
end)