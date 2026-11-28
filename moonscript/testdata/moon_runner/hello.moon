-- basic output, string interpolation, varargs and arg
name = "world"
print "hello #{name}"
print "args:", select("#", ...), ...
print "arg:", #arg, arg[0], arg[1], arg[2]
print "arg[-1] set:", arg[-1] != nil
for i, v in ipairs arg
  print i, v
count = (...) -> select "#", ...
print "count", count(...), count(nil, nil)
print type(_G.arg), _G.arg == arg
x = 10
print "number formatting", 1/3, 10/2, 2^53, 1e15, 1e100, -0.5, 100 * 1.1
print "returns are ignored"
"value"
