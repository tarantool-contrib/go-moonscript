-- a module found through package.moonpath (./?.moon -> ./lib/util.moon)
loaded_count = (loaded_count or 0) + 1

class Stack
  new: => @items = {}
  push: (v) => table.insert @items, v
  pop: => table.remove @items
  size: => #@items

{
  :Stack
  add: (a, b) -> a + b
  name: ...
  fail: (msg) -> error "util failed: #{msg}"
  index_nil: -> nil_value.field
}
