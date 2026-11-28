class Animal
  @count: 0
  new: (@name, @sound="...") =>
    @@count += 1
  speak: => "#{@name} says #{@sound}"
  __tostring: => "Animal(#{@name})"

class Dog extends Animal
  new: (name) =>
    super name, "woof"
  speak: =>
    "the dog: " .. super!
  fetch: (thing="ball") => "#{@name} fetches the #{thing}"

class Puppy extends Dog
  speak: => super\upper! .. "!"

a = Animal "cat", "meow"
d = Dog "rex"
p = Puppy "bit"
print a\speak!
print d\speak!
print p\speak!
print d\fetch!, d\fetch "stick"
print tostring(a), tostring(d)
print Animal.count, Dog.count, Animal.__name, Dog.__name, Dog.__parent == Animal
print p.__class.__parent.__parent.__name

-- class with properties, class-level statements, dynamic parent
class Counter
  value = 0
  increment: => value += 1
  get: => value
  @create: => @!

c1, c2 = Counter\create!, Counter!
c1\increment!
c2\increment!
print "shared", c1\get!, c2\get!

-- with, switch, comprehensions, destructuring
items = [i * i for i = 1, 6 when i % 2 == 0]
print table.concat items, ","
{first, second} = items
print first, second
squares = {k, k * k for k in *{1, 2, 3}}
print squares[1], squares[2], squares[3]
describe = (v) ->
  switch type v
    when "number" then "number #{v}"
    when "string" then "string #{v}"
    else "other"
print describe(1), describe("s"), describe({})
t = with {}
  .x = 1
  .y = 2
print t.x + t.y
s = "a,b,,c"
print table.concat [part for part in s\gmatch "([^,]*)"], "|"
print #[part for part in s\gmatch "([^,]+)"]
