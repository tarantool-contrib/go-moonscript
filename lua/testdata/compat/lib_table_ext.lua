-- PUC Lua 5.1: no table.move, table.new or table.clear (DialectLuaJIT only);
-- table.setn exists in Lua 5.1
print(table.move, package.preload["table.new"], package.preload["table.clear"])
print((pcall(require, "table.new")), (pcall(require, "table.clear")))
