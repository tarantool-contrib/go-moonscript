-- PUC Lua 5.1: string.rep ignores a separator; string.gfind is gmatch's alias
print(string.rep("ab", 3, ","), string.rep("ab", 2, {}), type(string.gfind))
for w in string.gfind("one two", "%a+") do print(w) end
