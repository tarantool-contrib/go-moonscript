local obj2 = {
  numbers = {
    1,
    2,
    3,
    4
  },
  properties = {
    color = "green",
    height = 13.5
  }
}
local first, second
first, second = obj2.numbers[1], obj2.numbers[2]
return print(first, second, color)