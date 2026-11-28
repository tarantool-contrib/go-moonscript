class Base
  value: 1
  new: (x) =>
    unused_in_ctor = x
    @x = x

  get: =>
    tmp = @x
    @x

class Child extends Base
  new: (...) =>
    super ...
    count = select "#", ...

  @static: (a, b) ->
    b

unused_class = class Unused
  method: => print "hi"

Child!
