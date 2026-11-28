class First
  new: (@var) =>
  show_var: => print "var is:", @var

class Second
  new: =>
    mixin self, First, "hi"

a = Second!
a\show_var!
