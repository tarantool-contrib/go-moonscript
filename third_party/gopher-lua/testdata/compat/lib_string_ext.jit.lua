-- LuaJIT 2.1: string.rep(s, n [, sep]), no string.gfind
print(string.gfind, type(string.gmatch))
for _, c in ipairs({{"ab", 3, ","}, {"ab", 0, ","}, {"ab", 1, ","}, {"ab", -1, ","}, {"ab", 2, ""},
  {"", 5, ","}, {"", 0, ","}, {"x", 3, "--"}, {"ab", 2.9, "|"}, {"ab", "2", 1}, {1, 2, 3}, {"a\0", 2, "\0"}}) do
  print("[" .. string.rep(c[1], c[2], c[3]) .. "]")
end
print(("x"):rep(3, "y"), string.rep("ab", 2, nil), #string.rep("x", 2^31), #string.rep("x", 2^32 + 3), #string.rep("x", -2^31))
print(pcall(string.rep, "x", 2^31 - 1, "y"))
print(pcall(string.rep, "xx", 2^30))
print((select(2, pcall(string.rep, "ab", 2, {})):match("string expected, got table")))
