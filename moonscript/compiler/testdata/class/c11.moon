class Foo extends Bar
  a: =>
    s = super
    t = -> super.x
    for v in *list
      super v
    super
  b: => @@c!
  new: => super!
  @static: => super\thing 1
