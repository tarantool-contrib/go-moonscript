moon = require "moon"
import p, run_with_scope, bind_methods, defaultbl, extend, copy, mixin, mixin_object, mixin_table, fold from moon

class Thing
  new: (@n) =>
  get: => @n
class Sub extends Thing

t = Thing 5
print "is_class", moon.is_class(Thing), moon.is_class(t), moon.is_class({})
print "is_instance", moon.is_instance(t), moon.is_instance(Thing), moon.is_instance({})
print "is_object", moon.is_object(t) == Thing, moon.is_object(5)
print "type", moon.type(t) == Thing, moon.type(Thing), moon.type(5), moon.type("s"), moon.type({})
s = Sub 1
print "is_instance_of", moon.is_instance_of(s, Thing), moon.is_instance_of(s, Sub), moon.is_instance_of(t, Sub)
print "is_subclass_of", moon.is_subclass_of(Sub, Thing), moon.is_subclass_of(Thing, Sub)
print "errors", pcall moon.is_instance_of, 5, Thing
print pcall moon.is_subclass_of, {}, Thing

-- p / dump (single key and array tables keep the output order fixed)
p {1, 2, {3}}
p "str", 42
p {name: "x"}
p t

-- run_with_scope
print run_with_scope (-> greeting .. " " .. tostring(print == nil)), {greeting: "hi"}
print run_with_scope ((a, b) -> x + a + b), {x: 100}, 1, 2

-- bind_methods
bound = bind_methods t
print "bound", bound.get!
getter = bound.get
print "bound again", getter!, bound.get == getter

-- defaultbl
fib = defaultbl {[0]: 0, [1]: 1}, (i) => self[i - 1] + self[i - 2]
print "fib", fib[30]
counts = defaultbl -> 0
counts.a += 1
counts.a += 1
print "counts", counts.a, counts.b

-- extend
a = {a: 1}
b = {b: 2}
c = {c: 3, a: "shadowed"}
extend a, b, c
print "extend", a.a, a.b, a.c, b.a
print "extend one", extend({})

-- copy
orig = {1, 2, 3, k: "v"}
cp = copy orig
cp[1] = 99
print "copy", orig[1], cp[1], cp[3], cp.k, cp != orig

-- mixin
class Mixer
  new: (@val) =>
  show: => "val=#{@val}"
obj = {}
mixin obj, Mixer, 7
print "mixin", obj\show!, obj.val

-- mixin_object
class Speaker
  speak: (word) => "speaker says #{word}"
spk = Speaker!
host = {}
mixin_object host, spk, {"speak"}
print "mixin_object", host\speak "hey"

-- mixin_table
dest = {}
mixin_table dest, {x: 1, y: 2, z: 3}, {"x", "z"}
print "mixin_table keys", dest.x, dest.y, dest.z
mixin_table dest, {y: 20}
print "mixin_table all", dest.y

-- fold
print "fold", fold({1, 2, 3, 4}, (a, b) -> a + b), fold({5}, (a, b) -> a * b), fold({}, (a, b) -> a)
print "fold max", fold {3, 9, 2}, math.max

-- debug.upvalue
make = ->
  hidden = 10
  -> hidden
f = make!
print "upvalue", moon.debug.upvalue(f, "hidden")
moon.debug.upvalue f, "hidden", 20
print "upvalue set", f!
print "upvalue missing", pcall moon.debug.upvalue, f, "nope"
print "debug index", moon.debug.getinfo == debug.getinfo
