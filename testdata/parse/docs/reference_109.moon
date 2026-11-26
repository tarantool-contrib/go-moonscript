i = 100

my_func = (using nil) ->
  i = "hello" -- a new local variable is created here

my_func!
print i -- prints 100, i is unaffected
