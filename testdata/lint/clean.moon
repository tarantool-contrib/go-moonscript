import insert from table

items = {}
insert items, 1

add = (x, y) -> x + y

class Point
  new: (@x, @y) =>
  __add: (other) => Point @x + other.x, @y + other.y

print add(1, 2), Point(1, 2), items
