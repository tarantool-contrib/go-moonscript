value = tonumber "5"

switch value
  when 1
    in_when = 1
  when 2, 3
    print "x"
  else
    in_switch_else = 2

unless value
  in_unless = 1

result = if value > 3
  big = true
  big
else
  "small"

items = {1, 2, 3}
first = items[1]
sliced = [i for i in *items[2,]]
evens = {k, v for k, v in pairs items when v % 2 == 0}

t = {}
t.field = 1
t[2] = 3
t.a.b = c_global

do
  local a, b
  a = 1

fn = (...) ->
  args = {...}
  #args

print result, first, sliced, evens, fn
