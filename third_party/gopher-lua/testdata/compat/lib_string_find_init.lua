-- string.find and string.match start at init clamped to the subject, as in
-- Lua 5.1's str_find_aux (gopher-lua: no match past the end, a plain find
-- past the end raised a Go "slice bounds out of range", an empty pattern
-- was found at 1 whatever init was); a find whose pattern has none of
-- 5.1's SPECIALS is a plain find; in gmatch a leading '^' matches '^'.
local function p(f, ...) print(pcall(function(...) return f(...) end, ...)) end
p(string.find, "abc", "b", 10)
p(string.find, "abc", "b", 10, true)
p(string.find, "abc", "", 10)
p(string.find, "abc", "", 2)
p(string.find, "abc", "", -1)
p(string.find, "abc", "", -10)
p(string.find, "abc", "$", 100)
p(string.find, "abc", "()", 5)
p(string.find, "abc", "c", 3, true)
p(string.find, "abc", "", 4, true)
p(string.find, "abc", "x*", 99)
p(string.find, "abc", "b", 1, false)
p(string.find, "abc", "b", 1, nil, "extra")
p(string.find, "a.c", ".", 1, 1)
p(string.find, "a)b", ")")
p(string.find, "a)b", "a)")
p(string.find, "a]b", "]")
p(string.match, "abc", "()", 10)
p(string.match, "abc", ".-$", 3)
p(string.match, "abc", "^", 100)
p(string.match, "abc", "c", -1)
p(string.match, "abc", "a", -100)
for w in string.gmatch("^a^b ab", "^%a") do print(w) end
for w in string.gmatch("x^y", "^") do print(w) end
print(select("#", string.gsub("^a", "^a", "x")))
