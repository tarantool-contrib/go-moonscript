local parse = require("moonscript.parse")
local compile = require("moonscript.compile")
local moon_code = [[(-> print "hello world")!]]
local tree, err = parse.string(moon_code)
if not (tree) then
  error("Parse error: " .. err)
end
local lua_code, pos
lua_code, err, pos = compile.tree(tree)
if not (lua_code) then
  error(compile.format_error(err, pos, moon_code))
end
return print(lua_code)