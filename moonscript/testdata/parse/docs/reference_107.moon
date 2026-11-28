my_object = {
  value: 1000
  write: => print "the value:", @value
}

run_callback = (func) ->
  print "running callback..."
  func!

-- this will not work:
-- the function has to no reference to my_object
run_callback my_object.write

-- function stub syntax
-- lets us bundle the object into a new function
run_callback my_object\write
