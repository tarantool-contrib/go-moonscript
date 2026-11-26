-- a Lua module requiring a MoonScript module
local util = require "lib.util"
return { sum = function(...) local s = 0; for _, v in ipairs({...}) do s = util.add(s, v) end; return s end }
