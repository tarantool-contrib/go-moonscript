-- LuaJIT 2.1: the jit module. What an interpreter-only LuaJIT shares with
-- the JIT-enabled reference: the version fields, jit.off/flush and their
-- argument checks, jit.attach's (TestJitModule checks the rest: status,
-- on, opt, util).
print(type(jit), package.loaded.jit == jit, require("jit") == jit)
print(jit.version, jit.version_num, type(jit.os), type(jit.arch))
print(jit.version_num >= 20100, jit.version:match("^LuaJIT 2%.1"))
for _, f in ipairs({"on", "off", "flush", "status", "attach"}) do
  print(f, type(jit[f]))
end
print(pcall(jit.off), pcall(jit.flush), pcall(jit.off, nil), pcall(jit.flush, nil))
print(pcall(jit.off, function() end), pcall(jit.flush, function() end, true), pcall(jit.on, function() end, false))
print(pcall(function() jit.off(true) end), pcall(function() jit.flush(true, true) end))
local function argerr(f, ...)
  local ok, e = pcall(f, ...)
  return ok, e and (e:match("function expected, got %a+") or e)
end
print(argerr(jit.off, 1), argerr(jit.on, "x"), argerr(jit.flush, {}), argerr(jit.off, false))
print(argerr(function() jit.off(print) end))
print(argerr(jit.on, true))
print(pcall(jit.attach, function() end), pcall(jit.attach, function() end, "bc"), pcall(jit.attach, print, 5))
print(argerr(jit.attach, 1))
print((select(2, pcall(jit.attach, print, {})):match("string expected, got table")))
