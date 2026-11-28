local item = {
  123,
  "shoe",
  "brown",
  123
}
local _, name, count
_, name, _, count = unpack(item)
return print(name, count)