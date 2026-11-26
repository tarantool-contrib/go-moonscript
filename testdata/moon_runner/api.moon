-- the moonscript modules from inside a script
moonscript = require "moonscript.base"
util = require "moonscript.util"
errors = require "moonscript.errors"
line_tables = require "moonscript.line_tables"

lua_code, ltable = moonscript.to_lua "x = 1\nprint x + 1\n"
print lua_code
print "line table", ltable[1], ltable[2]
print "to_lua error", moonscript.to_lua "x = = 1"
print "to_lua bad type", moonscript.to_lua 5

fn = moonscript.loadstring "return ...", "=(my chunk)"
print "loadstring", fn 1, 2
print "line table recorded", line_tables["=(my chunk)"] != nil
print "loadstring error", moonscript.loadstring "x ="
print "loadfile missing", moonscript.loadfile "nope.moon"
print "dofile", moonscript.dofile "lib/returns.moon"
print "create_moonpath", moonscript.create_moonpath "./?.lua;/a/?/init.lua;/b/?.so;/c/?.luac"
print "insert_loader again", moonscript.insert_loader!
print "dirsep", moonscript.dirsep

chunk = moonscript.loadstring "a = 1\nb = 2\n\nerror 'line four'", "@fake.moon"
ok, err = pcall chunk
print "runtime error", err
print "reverse", errors.reverse_line_number "lib/four_lines.moon", line_tables["@fake.moon"], tonumber(err\match ":(%d+):"), {}

print "truncate", errors.truncate_traceback "stack traceback:\n\ta:1: in function 'x'\n\tb:2: in function 'moonscript_chunk'\n\tc:3: in main chunk"
print "rewrite unchanged", errors.rewrite_traceback "stack traceback:\n\tnowhere:3: in main chunk", "nowhere:3: msg"
print "rewrite bad header", errors.rewrite_traceback "no traceback", "x"

print "util.trim", "[" .. util.trim("  a b  ") .. "]"
print "util.split", table.concat util.split("a,b,c", ","), "|"
print "util.pos_to_line", util.pos_to_line "a\nb\nc", 3
print "util.get_closest_line", util.get_closest_line "a\n\n\n", 3
print "util.mtype", util.mtype(5), util.mtype({})
print "util.safe_module", pcall -> util.safe_module("mod", {}).missing
print "util.get_options", util.get_options 1, 2, {opt: true}

parse = require "moonscript.parse"
compile = require "moonscript.compile"
tree = parse.string "print 'from tree'"
code = compile.tree tree
print code
print "parse error", parse.string "x = = 1"
print "compile error", compile.tree parse.string "@@ = 1"
print "version", require("moonscript.version").version
print "data", require("moonscript.data").lua_keywords["while"]
