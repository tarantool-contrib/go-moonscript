class List 
  add: (item) => print "adding to", self
  remove: (item) => print "removing from", self

class Encapsulation
  new: =>
    @list = List!
    mixin_object self, @list, {"add", "remove"}

e = Encapsulation!
e.add "something"
