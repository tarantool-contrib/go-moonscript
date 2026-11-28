-- Drives moonscript.cmd.coverage with a hook of its own (upstream moon -c's
-- hook, a tail call, printed an empty report on Lua 5.1); the report covers
-- this file: which lines ran, as mapped back from Lua lines by the line
-- table.
coverage = require "moonscript.cmd.coverage"
cov = coverage.CodeCoverage!
hook = (ev, line) ->
  info = debug.getinfo 2, "S"
  cov.line_counts[info.source][line] += 1

debug.sethook hook, "l"

add = (a, b) ->
  a + b

class Counter
  new: (@n = 0) =>
  inc: =>
    @n += 1
    @n

c = Counter!
for i = 1, 3
  c\inc!

if c.n > 2
  print "big", c.n
else
  print "small"

never = ->
  print "never called"

items = [x * 2 for x in *{1, 2, 3}]
t = {k, v for k, v in pairs {a: 1}}

while c.n < 5
  c\inc!

s = switch c.n
  when 5
    "five"
  else
    "other"

long_call = add 1,
  2

tbl = {
  a: 1
  b: ->
    "b"
  {
    nested: true
  }
}

ok, err = pcall ->
  error "boom"

with_do = do
  x = 10
  x * 2

unless false
  print "unless"

co = coroutine.wrap ->
  coroutine.yield 1
  2
co!
co!

debug.sethook!
print s, add(1, 2), #items, long_call, with_do, ok
cov\print_results!
