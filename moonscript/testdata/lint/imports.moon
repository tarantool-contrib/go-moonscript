import insert, remove from table
import concat from table
insert {}, 1

remove = nil
concat ..= "x"

f = ->
  insert = 5
  insert

do
  import insert from require "other"
  insert 2

g = ->
  import concat from table
  concat

local remove2
h = ->
  import remove2 from table
  remove2

{insert, z} = {1, 2}

import \colon_name from obj
colon_name!

print f!, g!, h!, z
