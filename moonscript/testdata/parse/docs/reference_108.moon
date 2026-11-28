i = 100

-- many lines of code...

my_func = ->
  i = 10
  while i > 0
    print i
    i -= 1

my_func!

print i -- will print 0
