local to_lua
to_lua = require("moonscript.base").to_lua
local lua_code, line_table = to_lua([[x = 124
print "hello world #{x}"
]])