package compiler

// Snippet -> Lua expectations for transform_statement.go, obtained from the
// reference moonc v0.8.0.

var stmSnippetCases = []struct{ name, src, want string }{
	{"c01", `for x in *a
  continue if x > 2
  print x`, `local _list_0 = a
for _index_0 = 1, #_list_0 do
  local _continue_0 = false
  repeat
    local x = _list_0[_index_0]
    if x > 2 then
      _continue_0 = true
      break
    end
    print(x)
    _continue_0 = true
  until true
  if not _continue_0 then
    break
  end
end`},
	{"c02", `for i=1,10
  if i % 2 == 0
    continue
  print i`, `for i = 1, 10 do
  local _continue_0 = false
  repeat
    if i % 2 == 0 then
      _continue_0 = true
      break
    end
    print(i)
    _continue_0 = true
  until true
  if not _continue_0 then
    break
  end
end`},
	{"c03", `while true
  continue`, `while true do
  local _continue_0 = false
  repeat
    do
      _continue_0 = true
      break
    end
    _continue_0 = true
  until true
  if not _continue_0 then
    break
  end
end`},
	{"c04", `x = for i in *a
  continue if i
  i`, `local x
do
  local _accum_0 = { }
  local _len_0 = 1
  local _list_0 = a
  for _index_0 = 1, #_list_0 do
    local _continue_0 = false
    repeat
      local i = _list_0[_index_0]
      if i then
        _continue_0 = true
        break
      end
      local _value_0 = i
      _accum_0[_len_0] = _value_0
      _len_0 = _len_0 + 1
      _continue_0 = true
    until true
    if not _continue_0 then
      break
    end
  end
  x = _accum_0
end`},
	{"c05", `for x in *a
  for y in *b
    continue if y
  continue if x
  print x`, `local _list_0 = a
for _index_0 = 1, #_list_0 do
  local _continue_0 = false
  repeat
    local x = _list_0[_index_0]
    local _list_1 = b
    for _index_1 = 1, #_list_1 do
      local _continue_1 = false
      repeat
        local y = _list_1[_index_1]
        if y then
          _continue_1 = true
          break
        end
        _continue_1 = true
      until true
      if not _continue_1 then
        break
      end
    end
    if x then
      _continue_0 = true
      break
    end
    print(x)
    _continue_0 = true
  until true
  if not _continue_0 then
    break
  end
end`},
	{"c07", `for x in *a[2,]
  print x
for x in *a[,-1]
  print x
for x in *t.list[2,n,2]
  print x
for {a, b} in *pairs
  print a, b
for k, {x, y} in pairs t
  print x`, `local _list_0 = a
for _index_0 = 2, #_list_0 do
  local x = _list_0[_index_0]
  print(x)
end
local _list_1 = a
local _max_0 = -1
for _index_0 = 1, _max_0 < 0 and #_list_1 + _max_0 or _max_0 do
  local x = _list_1[_index_0]
  print(x)
end
local _list_2 = t.list
local _max_1 = n
for _index_0 = 2, _max_1 < 0 and #_list_2 + _max_1 or _max_1, 2 do
  local x = _list_2[_index_0]
  print(x)
end
local _list_3 = pairs
for _index_0 = 1, #_list_3 do
  local _des_0 = _list_3[_index_0]
  local a, b
  a, b = _des_0[1], _des_0[2]
  print(a, b)
end
for k, _des_0 in pairs(t) do
  local x, y
  x, y = _des_0[1], _des_0[2]
  print(x)
end`},
	{"c08", `a.b[c][d] += 1
x.y ..= "s"
a[f!] -= b + c
z or= 4`, `local _update_0, _update_1 = c, d
a.b[_update_0][_update_1] = a.b[_update_0][_update_1] + 1
x.y = x.y .. "s"
local _update_2 = f()
a[_update_2] = a[_update_2] - (b + c)
local z = z or 4`},
	{"c09", `if a = f!
  print a
elseif b = g!
  print b
else
  print c
x = if c = h!
  c
elseif d = k!
  d`, `do
  local a = f()
  if a then
    print(a)
  else
    do
      local b = g()
      if b then
        print(b)
      else
        print(c)
      end
    end
  end
end
local x
do
  local c = h()
  if c then
    x = c
  else
    do
      local d = k()
      if d then
        x = d
      end
    end
  end
end`},
	{"c10", `with a
  .x = 1
with b = c
  .y = 2
with t.x = d
  .z = 3
x = with foo!
  \bar!
f = ->
  with obj
    return 4`, `do
  local _with_0 = a
  _with_0.x = 1
end
do
  local b = c
  b.y = 2
end
do
  local _with_0 = d
  t.x = _with_0
  _with_0.z = 3
end
local x
do
  local _with_0 = foo()
  _with_0:bar()
  x = _with_0
end
local f
f = function()
  do
    local _with_0 = obj
    return 4
  end
end`},
	{"c12", `export *
export ^
x = 1
Foo = 2
local *
y = 3
local ^
Bar = 4
baz = 5`, `x = 1
Foo = 2
y = 3
Bar = 4
baz = 5`},
	{"c13", `export a, b = 1, 2
export class Foo
export x`, `a, b = 1, 2
do
  local _class_0
  local _base_0 = { }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function() end,
    __base = _base_0,
    __name = "Foo"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  Foo = _class_0
end`},
	{"c14", `x = switch y
  when 1, 2 or 3
    "a"
  when f!
    "b"
  else
    "c"
f = ->
  switch y
    when 1
      "a"`, `local x
local _exp_0 = y
if 1 == _exp_0 or (2 or 3) == _exp_0 then
  x = "a"
elseif f() == _exp_0 then
  x = "b"
else
  x = "c"
end
local f
f = function()
  local _exp_1 = y
  if 1 == _exp_1 then
    return "a"
  end
end`},
	{"c15", `unless a = f!
  print a
unless x
  print 1
else
  print 2
f = -> return if x then 1 else 2
g = -> return a\b
h = -> return [i for i in *t]`, `do
  local a = f()
  if not a then
    print(a)
  end
end
if not (x) then
  print(1)
else
  print(2)
end
local f
f = function()
  if x then
    return 1
  else
    return 2
  end
end
local g
g = function()
  local _base_0 = a
  local _fn_0 = _base_0.b
  return function(...)
    return _fn_0(_base_0, ...)
  end
end
local h
h = function()
  local _accum_0 = { }
  local _len_0 = 1
  local _list_0 = t
  for _index_0 = 1, #_list_0 do
    local i = _list_0[_index_0]
    _accum_0[_len_0] = i
    _len_0 = _len_0 + 1
  end
  return _accum_0
end`},
	{"c16", `import a, \b, c from require "x"
import d from e`, `local a, b, c
do
  local _obj_0 = require("x")
  a, b, c = _obj_0.a, (function()
    local _base_0 = _obj_0
    local _fn_0 = _base_0.b
    return function(...)
      return _fn_0(_base_0, ...)
    end
  end)(), _obj_0.c
end
local d
d = e.d`},
	{"c17", `x = do
  print 1
  2
f = -> do
  3
y = a\b
{p, q} = a\b`, `local x
do
  print(1)
  x = 2
end
local f
f = function()
  do
    return 3
  end
end
local y
do
  local _base_0 = a
  local _fn_0 = _base_0.b
  y = function(...)
    return _fn_0(_base_0, ...)
  end
end
local p, q
do
  local _base_0 = a
  local _fn_0 = _base_0.b
  do
    local _obj_0
    _obj_0 = function(...)
      return _fn_0(_base_0, ...)
    end
    p, q = _obj_0[1], _obj_0[2]
  end
end`},
	{"c18", `if {a, b} = f!
  print a
elseif c
  print c
f = -> if x = y! then x else 3`, `do
  local _des_0 = f()
  if _des_0 then
    local a, b
    a, b = _des_0[1], _des_0[2]
    print(a)
  elseif c then
    print(c)
  end
end
local f
f = function()
  do
    local x = y()
    if x then
      return x
    else
      return 3
    end
  end
end`},
	{"d01", `print x if y else z
x = 2 if y
x = 2 unless y
print i for i in *a when i > 2
{a, b} = f! if y`, `if y then
  print(x)
else
  local _scrap_0 = z
end
local x
if y then
  x = 2
end
if not (y) then
  x = 2
end
local _list_0 = a
for _index_0 = 1, #_list_0 do
  local i = _list_0[_index_0]
  if i > 2 then
    print(i)
  end
end
local a, b
if y then
  do
    local _obj_0 = f()
    a, b = _obj_0[1], _obj_0[2]
  end
end`},
	{"d02", `f = ->
  if a = f!
    a
  elseif b = g!
    b
  elseif c
    c
  else
    d`, `local f
f = function()
  do
    local a = f()
    if a then
      return a
    else
      do
        local b = g()
        if b then
          return b
        elseif c then
          return c
        else
          return d
        end
      end
    end
  end
end`},
	{"d03", `f = -> return 1, 2
g = -> return (if x then 1)
h = -> return
k = -> with x
  .y = 1`, `local f
f = function()
  return 1, 2
end
local g
g = function()
  return ((function()
    if x then
      return 1
    end
  end)())
end
local h
h = function() end
local k
k = function()
  do
    local _with_0 = x
    _with_0.y = 1
    return _with_0
  end
end`},
	{"d04", `@x += 1
a.b += 1
t[i][j] *= 2
@@y -= 1
x = 1
x += f! + 1`, `self.x = self.x + 1
a.b = a.b + 1
local _update_0, _update_1 = i, j
t[_update_0][_update_1] = t[_update_0][_update_1] * 2
self.__class.y = self.__class.y - 1
local x = 1
x = x + (f() + 1)`},
	{"d05", `for x in *a
  with x
    continue if .skip
    print .name
for x in *a
  switch x
    when 1
      continue
    else
      print x
for i=1,3
  f = ->
    for j=1,2
      continue
  print i`, `local _list_0 = a
for _index_0 = 1, #_list_0 do
  local _continue_0 = false
  repeat
    local x = _list_0[_index_0]
    do
      if x.skip then
        _continue_0 = true
        break
      end
      print(x.name)
    end
    _continue_0 = true
  until true
  if not _continue_0 then
    break
  end
end
local _list_1 = a
for _index_0 = 1, #_list_1 do
  local _continue_0 = false
  repeat
    local x = _list_1[_index_0]
    local _exp_0 = x
    if 1 == _exp_0 then
      _continue_0 = true
      break
    else
      print(x)
    end
    _continue_0 = true
  until true
  if not _continue_0 then
    break
  end
end
for i = 1, 3 do
  local f
  f = function()
    for j = 1, 2 do
      local _continue_0 = false
      repeat
        do
          _continue_0 = true
          break
        end
        _continue_0 = true
      until true
      if not _continue_0 then
        break
      end
    end
  end
  print(i)
end`},
	{"d07", `x = with t = {}
  .a = 1
{a, b} = with c
  .d = 1
y = switch z
  when 1 then "a"
f = -> x = if c then 1 else 2`, `local x
do
  local t = { }
  t.a = 1
  x = t
end
local a, b
do
  local _with_0 = c
  _with_0.d = 1
  a, b = _with_0[1], _with_0[2]
end
local y
local _exp_0 = z
if 1 == _exp_0 then
  y = "a"
end
local f
f = function()
  if c then
    x = 1
  else
    x = 2
  end
end`},
	{"d08", `x, y = if c then 1, 2
a = b\c d
print "x" unless y`, `local x, y
if c then
  x, y = 1, 2
end
local a = b:c(d)
if not (y) then
  return print("x")
end`},
	{"d09", `local x
x = if a then [i for i in *b] else {}
z = for i in *t
  i * 2
w = while true
  break`, `local x
if a then
  do
    local _accum_0 = { }
    local _len_0 = 1
    local _list_0 = b
    for _index_0 = 1, #_list_0 do
      local i = _list_0[_index_0]
      _accum_0[_len_0] = i
      _len_0 = _len_0 + 1
    end
    x = _accum_0
  end
else
  x = { }
end
local z
do
  local _accum_0 = { }
  local _len_0 = 1
  local _list_0 = t
  for _index_0 = 1, #_list_0 do
    local i = _list_0[_index_0]
    _accum_0[_len_0] = i * 2
    _len_0 = _len_0 + 1
  end
  z = _accum_0
end
local w
do
  local _accum_0 = { }
  local _len_0 = 1
  while true do
    local _value_0
    break
    _accum_0[_len_0] = _value_0
    _len_0 = _len_0 + 1
  end
  w = _accum_0
end`},
	{"d10", `f = (...) ->
  for x in *{...}
    print x
for x in *f![1,2]
  print x
for x in *a[f!,g!]
  print x`, `local f
f = function(...)
  local _list_0 = {
    ...
  }
  for _index_0 = 1, #_list_0 do
    local x = _list_0[_index_0]
    print(x)
  end
end
local _list_0 = f()
local _max_0 = 2
for _index_0 = 1, _max_0 < 0 and #_list_0 + _max_0 or _max_0 do
  local x = _list_0[_index_0]
  print(x)
end
local _list_1 = a
local _max_1 = g()
for _index_0 = f(), _max_1 < 0 and #_list_1 + _max_1 or _max_1 do
  local x = _list_1[_index_0]
  print(x)
end`},
	{"d11", `export ^
class Foo
class bar
Baz = 1`, `do
  local _class_0
  local _base_0 = { }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function() end,
    __base = _base_0,
    __name = "Foo"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  Foo = _class_0
end
local bar
do
  local _class_0
  local _base_0 = { }
  _base_0.__index = _base_0
  _class_0 = setmetatable({
    __init = function() end,
    __base = _base_0,
    __name = "bar"
  }, {
    __index = _base_0,
    __call = function(cls, ...)
      local _self_0 = setmetatable({}, _base_0)
      cls.__init(_self_0, ...)
      return _self_0
    end
  })
  _base_0.__class = _class_0
  bar = _class_0
end
Baz = 1`},
	{"d12", `do
  local *
  a = 1
  b = ->
    c = 2
  {d, e} = t`, `do
  local a, b, d, e
  a = 1
  b = function()
    local c = 2
  end
  do
    local _obj_0 = t
    d, e = _obj_0[1], _obj_0[2]
  end
end`},
	{"d13", `if x
  print 1
elseif y
  print 2
x = unless a
  1
else
  2`, `if x then
  print(1)
elseif y then
  print(2)
end
local x
if not (a) then
  x = 1
else
  x = 2
end`},
	{"d14", `f = ->
  for x in *a
    continue
g = ->
  while x
    return 4 if x
    continue if y
    x!`, `local f
f = function()
  local _list_0 = a
  for _index_0 = 1, #_list_0 do
    local _continue_0 = false
    repeat
      do
        local x = _list_0[_index_0]
        _continue_0 = true
        break
      end
      _continue_0 = true
    until true
    if not _continue_0 then
      break
    end
  end
end
local g
g = function()
  while x do
    local _continue_0 = false
    repeat
      if x then
        return 4
      end
      if y then
        _continue_0 = true
        break
      end
      x()
      _continue_0 = true
    until true
    if not _continue_0 then
      break
    end
  end
end`},
}
