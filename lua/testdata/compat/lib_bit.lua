-- PUC Lua 5.1 has no bit library (LuaJIT's, DialectLuaJIT only)
print(bit, package.loaded.bit, (pcall(require, "bit")))
