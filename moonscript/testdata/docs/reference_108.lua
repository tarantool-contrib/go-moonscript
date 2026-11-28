local obj = {
  hello = "world",
  day = "tuesday",
  length = 20
}
local hello, the_day
hello, the_day = obj.hello, obj.day
return print(hello, the_day)