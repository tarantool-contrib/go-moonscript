parse = require "moonscript.parse"
compile = require "moonscript.compile"

moon_code = [[(-> print "hello world")!]]

tree, err = parse.string moon_code
unless tree
  error "Parse error: " .. err

lua_code, err, pos = compile.tree tree
unless lua_code
  error compile.format_error err, pos, moon_code

-- our code is ready
print lua_code
