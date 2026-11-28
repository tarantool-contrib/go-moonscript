local moonscript = require("moonscript.base")
local fn = moonscript.loadstring("10")
print(fn())
fn = moonscript.loadstring("10", {
  implicitly_return_root = false
})
return print(fn())