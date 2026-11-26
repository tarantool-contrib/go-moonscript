-- Lua 5.1 library behavior seen through moon
print string.format "%5.2f|%d|%s|%q", 3.14159, 42, "str", "a\nb"
print ("abc")\rep(3), ("Hello")\upper!, ("x=1, y=2")\gsub "(%w+)=(%w+)", "%2=%1"
print string.find("hello world", "o w"), string.match "key = value", "(%w+)%s*=%s*(%w+)"
print select(2, "a", "b", "c")
print table.concat {1, 2, 3}, "-"
t = {5, 2, 8, 1}
table.sort t
print table.concat t, " "
table.sort t, (a, b) -> a > b
print table.concat t, " "
print unpack {1, 2, 3}
print math.floor(3.7), math.max(1, 5, 3), math.min(4, 2), math.abs(-3)
print tonumber("0x10"), tonumber("12", 8), tonumber("z", 36), tonumber("nope")
print tostring(nil), tostring(true), tostring(12), tostring(1.5)
print #"length", #{1, 2, 3}
print type(print), type(nil), type({}), type("")
print rawequal(t, t), rawlen and "has rawlen" or "no rawlen"
print next({}), type(next({1}))
s = 0
for k, v in pairs {10, 20, 30}
  s += v
print "pairs sum", s
print "varargs", ((...) -> select "#", ...)(1, nil, 3)
print "string coercion", "10" + 5, 10 .. 20
print "integer division", 7 / 2, 7 % 3, -7 % 3, 2 ^ 10
print "getfenv", getfenv(1) == _G, _VERSION
mt = setmetatable {}, __index: (k) => k .. "!"
print mt.hello
print "os.time type", type os.time!
print "string.byte", string.byte("A"), string.char(72, 105)
