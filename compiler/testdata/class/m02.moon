class Foo extends Bar.Baz!
  and: => 1
  @or: 2
  new: (@a=1, b=2, ...) =>
    super b, ...
