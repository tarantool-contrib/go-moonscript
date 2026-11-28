-- string.gmatch returns an iterator closure that takes no arguments
local it = string.gmatch("asdf", "a")
print(type(it), it(), it(), it())
local words = string.gmatch("one two  three", "%a+")
print(words(), words(), words(), words())
for k, v in string.gmatch("a=1, b=2", "(%w+)=(%w+)") do print(k, v) end
for p, w in string.gmatch("ab cd", "()(%a+)") do print(p, w) end
local n = 0
for e in string.gmatch("abc", "") do n = n + 1 end
print(n)
for c in string.gmatch("abc", "[ac]") do print(c) end
print(select("#", string.gmatch("x", "x")))
local f = string.gfind or string.gmatch
for w in f("one two", "%a+") do print(w) end
