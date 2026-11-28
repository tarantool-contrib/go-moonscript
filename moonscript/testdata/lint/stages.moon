import insert from table
insert = 5
do
  unused_var = 1
missing_global 10
f = ->
  import insert from table
  insert
f!
