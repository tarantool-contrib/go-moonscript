if hello = os.getenv "hello"
  print "You have hello", hello
elseif world = os.getenv "world"
  print "you have world", world
else
  print "nothing :("
