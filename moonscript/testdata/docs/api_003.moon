moonscript = require "moonscript.base"

fn = moonscript.loadstring "10"
print fn! -- prints "10"

fn = moonscript.loadstring "10", implicitly_return_root: false
print fn! -- prints nothing
