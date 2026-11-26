class Bar extends Foo
  new: (a) =>
    super a
    super.thing @
  hi: (x) => super x
  other: => super\hello 1, 2
  stub: => super\hello
  @cm: => super!
  [k]: => super!
  "str": => super 1
