import to_lua from require "moonscript.base"

lua_code, line_table = to_lua [[
x = 124
print "hello world #{x}"
]]
