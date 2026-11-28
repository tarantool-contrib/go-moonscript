-- table.concat (go-moonscript patch: the result is built directly
-- instead of on the Lua stack, which overflowed for big tables).
local t = {"a", "b", 3, 4.5, -6, "", "z"}
print(table.concat(t), table.concat(t, ", "), table.concat(t, "", 2, 4))
print(table.concat(t, "-", 3), table.concat(t, "-", 7, 7), "[" .. table.concat(t, "-", 8) .. "]")
print("[" .. table.concat({}) .. "]", "[" .. table.concat({}, "x") .. "]", table.concat({1}, "x"))
print(table.concat({"x", "y"}, ""), table.concat(t, "", 5, 4) == "")
print((pcall(table.concat, {1, {}, 3})), (pcall(table.concat, {1, 2, true}, ",")))
-- big tables
local big = {}
for i = 1, 100000 do big[i] = i % 10 end
local s = table.concat(big)
print(#s, s:sub(1, 12), s:sub(-5))
s = table.concat(big, ", ", 99990)
print(#s, s)
local words = {}
for i = 1, 30000 do words[i] = "w" .. i end
print(#table.concat(words, " "))
-- Lua 5.1's tconcat: a number separator, i and j not clamped to 1..#t
print(table.concat({1, 2, 3}, 0), table.concat({1, 2}, 1.5))
print(pcall(table.concat, {1, 2, 3}, ",", 1, 5))
print(pcall(table.concat, {1, 2, 3}, ",", 0))
print(table.concat({1, 2, 3}, ",", 3, 2) == "", table.concat({[0] = 'a', 'b'}, ",", 0, 1))
print(pcall(table.concat, {1, {}, 3}))
print(table.concat({1.5, 2^53, 1e100, -0.25}, " "))
