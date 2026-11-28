-- string.match returns nil when there is no match
print(select("#", string.match("abc", "%d")), string.match("abc", "%d"))
print(select("#", ("abc"):match("(%d+)")), ("abc"):match("(%d+)"))
print(tonumber(("abc"):match("(%d+)")))
print(string.match("abc", "b", 10), string.match("abc", "^b", 2), string.match("abc", "x", -1))
local t = {string.match("abc", "z"), "after"}
print(#t, t[1], t[2])
print(string.match("key=val", "(%w+)=(%w+)"))
print(string.match("hello", "()ll()"))
