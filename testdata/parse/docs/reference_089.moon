-- some object
my_module =
  state: 100
  add: (value) =>
    self.state + value

import \add from my_module

print add 22 -- equivalent to calling my_module\add 22
