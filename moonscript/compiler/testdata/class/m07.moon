x = class A extends B
y.z = class extends Q
  m: => super!
class Foo
  @@x = 1
  @y = => @@x
  local a, b
  local c
  print a, b, c
  z: => a + c
