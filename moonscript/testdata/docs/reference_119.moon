tmp = 1213
i, k = 100, 50

my_func = (add using k,i) ->
  tmp = tmp + add -- a new local tmp is created
  i += tmp
  k += tmp

my_func(22)
print i,k -- these have been updated
