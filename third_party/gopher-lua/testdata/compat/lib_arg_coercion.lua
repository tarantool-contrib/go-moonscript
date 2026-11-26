-- the library functions take numeric strings for integer and number
-- arguments and numbers for string arguments (luaL_checkinteger,
-- luaL_optinteger, luaL_optnumber, luaL_optstring)
print(string.rep("x", "3"), ("x"):rep(" 2 "), string.rep("ab", "0x2"))
print(string.sub("hello", "2", "-2"), string.byte("abc", "2"), string.char("72", "105"))
print(string.find("abcabc", "b", "3"), string.match("abc", ".", "-1"))
print(select("2", "a", "b", "c"), select("-1", "a", "b"), select("#", 1, 2), select("#x", 1, 2))
print(math.random("1") == 1, math.max("10", 2), math.floor("2.5"), math.fmod("7", "3"))
print(table.concat({1, 2, 3}, 0, "2", "3"), table.concat({1, 2, 3}, ", ", "1"))
local t = {1, 2, 3}
table.insert(t, "2", "x")
print(t[1], t[2], t[3], t[4], table.remove(t, "1"), t[1])
print(unpack({1, 2, 3}, "2"), unpack({1, 2, 3}, "1", "2"))
print(tonumber("7", "8"), tostring(12):rep(2))
print(os.date("!%Y", "86400"), os.time({year = "2000", month = "1", day = "1", hour = "0"}) ~= nil)
print(string.format("%s|%5s", 1, 2), string.rep(1, 3))
print(pcall(string.rep, "x", "y") == false, pcall(string.rep, "x", {}) == false, (pcall(select, "x")))
print((pcall(string.sub, "abc", "1x")), (pcall(math.random, "1.5x")))
