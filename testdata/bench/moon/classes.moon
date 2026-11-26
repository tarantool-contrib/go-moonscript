-- Class-heavy method dispatch: a shape hierarchy with inheritance, super
-- calls, class methods and a visitor, all through MoonScript's class
-- machinery (__index chains, __base, __parent).
N = tonumber(arg and arg[1]) or 1000

class Shape
  @count: 0
  new: (@name) => @@count += 1
  area: => 0
  perimeter: => 0
  scale: (k) => @
  describe: => "#{@name}:#{@area!}"
  accept: (v) => v\visit_shape @

class Rect extends Shape
  new: (@w, @h) => super "rect"
  area: => @w * @h
  perimeter: => 2 * (@w + @h)
  scale: (k) => Rect @w * k, @h * k
  accept: (v) => v\visit_rect @

class Square extends Rect
  new: (s) =>
    super s, s
    @name = "square"
  scale: (k) => Square @w * k
  perimeter: => 4 * @w

class Circle extends Shape
  new: (@r) => super "circle"
  area: => 3 * @r * @r
  perimeter: => 6 * @r
  scale: (k) => Circle @r * k
  accept: (v) => v\visit_circle @

class Visitor
  new: => @total = 0
  visit_shape: (s) => @total += s\area!
  visit_rect: (r) => @visit_shape r
  visit_circle: (c) =>
    @total += c\perimeter!
    @visit_shape c

class Counter extends Visitor
  new: =>
    super!
    @n = 0
  visit_shape: (s) =>
    @n += 1
    super s

shapes = {}
for i = 1, 100
  m = i % 3
  shapes[i] = if m == 0
    Rect i % 7 + 1, i % 5 + 1
  elseif m == 1
    Square i % 9 + 1
  else
    Circle i % 4 + 1

total, per, len = 0, 0, 0
v = Counter!
for _ = 1, N
  for s in *shapes
    t = s\scale 2
    total += t\area!
    per += t\perimeter!
    s\accept v
  len += #shapes[N % 100 + 1]\describe!

print total, per, len, v.total, v.n, Rect.count + Square.count + Circle.count
