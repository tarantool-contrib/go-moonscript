-- PUC Lua 5.1 has no jit module (DialectLuaJIT only)
print(jit, package.loaded.jit, (pcall(require, "jit")))
