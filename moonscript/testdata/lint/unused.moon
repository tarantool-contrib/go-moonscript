-- unused names in every kind of scope
a, b, c = 1, 2, 3
print b

x = 10
x = 20

_ = "ignored"

f = (arg, other) ->
  inner = arg * 2
  used = other
  print used
  shadow = 1
  g = ->
    shadow = 2
    nested_unused = 3
  g!

local declared
declared = 5

{p, q} = {1, 2}
print p

for i = 1, 10
  loop_unused = i

for k, v in pairs {}
  pair_unused = k

squares = [n * n for n in *{1, 2, 3}]

if f
  in_if = 1
elseif x
  in_elseif = 2
else
  in_else = 3

while false
  in_while = 1

h = ->
  later = 1
  ->
    print later

h!
