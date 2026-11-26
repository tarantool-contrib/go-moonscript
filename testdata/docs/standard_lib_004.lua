local a = {
  hello = "world"
}
local b = {
  okay = "sure"
}
extend(a, b)
return print(a.okay)