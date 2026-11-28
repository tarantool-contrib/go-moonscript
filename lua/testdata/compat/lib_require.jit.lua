-- require is ll_require with Lua 5.1's loaders (preload, package.path,
-- package.cpath, the C root): "module 'x' not found:" lists what each
-- loader tried, a file that fails to load is "error loading module 'x'
-- from file 'f':\n\t<msg>", and the loop message; package.loaders is read
-- from the package table at each call. No C library is found here (cpath
-- names no file that exists). The .jit.lua copy has luajit's results.
package.path = "files/?.lua;;files/?/init.lua"
package.cpath = "files/?.so;files/lib?.so"
local function req(label, name)
  print(label, pcall(require, name))
end
req("missing", "nosuchmod")
req("missing dotted", "a.b")
req("syntax error", "req_syn")
req("runtime error", "req_rt")
req("runtime error again", "req_rt")
req("loop", "req_loop")
local ok, m = pcall(require, "req_ok")
print("found", ok, m.name, m.src, package.loaded.req_ok == m)
print("found again", require("req_ok") == m)
req("init", "pkg")
req("shebang", "req_sheb")
print("no value", require("req_nil"), package.loaded.req_nil, require("req_nil"), req_nil_runs)
package.preload.pre = function(...) return "preloaded " .. select("#", ...) .. " " .. ... end
req("preload", "pre")
package.loaded.preset = "set"
req("loaded", "preset")

-- the error's position is require's caller's
print(pcall(function() local r = require("nosuchmod2") return r end))

-- package.* of the wrong type
package.path = "files/?.lua"
package.cpath = 5
req("cpath number", "nosuchmod3")
package.path = "?"
package.cpath = ""
req("directory", "files")
package.path = nil
req("path nil", "nosuchmod4")
package.path = "files/?.lua"
local preload = package.preload
package.preload = 1
req("preload not a table", "nosuchmod5")
package.preload = preload
local loaders = package.loaders
package.loaders = {}
req("no loaders", "nosuchmod6")
package.loaders = { function(n) return "\n\tmine " .. n end, function() return 7 end,
  function() return true end, function() end }
req("loader messages", "nosuchmod7")
package.loaders = nil
req("loaders nil", "nosuchmod8")
package.loaders = loaders
local pkg = package
package = nil
print("package replaced", pcall(require, "nosuchmod9"))
package = pkg
req("require of 1", 1)
print("require of nil", pcall(require))
