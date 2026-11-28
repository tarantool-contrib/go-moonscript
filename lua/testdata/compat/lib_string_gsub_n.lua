-- string.gsub with n < 1 replaces nothing, as in Lua 5.1 (gopher-lua
-- replaced every match when the subject's first position matched).
print(string.gsub("aaa", "a", "b", 0))
print(string.gsub("aaa", "a", "b", -1))
print(string.gsub("aaa", "a", "b", -3.5))
print(string.gsub("aaa", "a", "b", 0.5))
print(string.gsub("aaa", "", "-", 0))
print(string.gsub("baa", "a", "b", 0))
print(string.gsub("aaa", "a", "b", 1.9))
print(string.gsub("aaa", "a", "b", "2"))
print(string.gsub("aaa", "a", "b", nil))
print(string.gsub("aaa", "(", "b", 0))
