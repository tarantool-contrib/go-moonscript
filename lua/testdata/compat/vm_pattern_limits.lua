-- Pattern matching robustness, against Lua 5.1: greedy and lazy repeats
-- over long subjects (gopher-lua recursed once per repeated byte and gave
-- up with "pattern/input too complex" past a million), at most 32
-- captures ("too many captures"; nesting was unbounded), and back
-- references to unfinished or position captures (Go slice panics, or a
-- match where Lua 5.1 raises "invalid capture index").
local function t(p, s, init)
  local label = #p > 60 and ("pattern of " .. #p .. " bytes") or p
  print(label, pcall(function() return string.find(s, p, init) end))
end
local s = string.rep("a", 2000000)
print(string.find(s, ".*"))
print(string.find(s, "a-$"))
print(select("#", string.find(s, "(a*)")), #select(3, string.find(s, "(a*)")))
print(string.gsub(s, "a+", "b"))
print(#string.match(s .. "b", "^[a]*b"))
print(string.find(s .. "b", "a-b"))
print(string.find(s, "a+a"))
print(string.find("xaaab", "a-ab"))
print(string.find("xaaab", "a*ab"))
print(string.find("xaaab", "x+a?a+b"))
print(string.find("", "a*"))
print(string.find("bbb", "a-", 2))
print(string.match("key = value", "(%w+)%s*=%s*(%w+)"))
print(string.gsub("hello world", "o+", "0"))
print(string.gsub("abc", "%w-", "-"))
print(string.gsub("abc", "%w*", "-"))
t("(ab)%1", "abab")
t("(a*)b%1", "aabaa")
t("()ab%1", "abab")
t("(a(b%1))", "ab")
t("%1(a)", "aa")
t("(a)%2", "aa")
t("z(a%1)", "xy")
t("[a-c]+[a-c]()%1?$", "ab1bb2aba", 4)
t("[^a]*(((%s*%%?%1%bab)%d(%a-[^a]*[ab]?))%bab-)%d", "b22cc")
t(string.rep("(", 33) .. "a" .. string.rep(")", 33), "a")
print(select("#", string.find("a", string.rep("(", 32) .. "a" .. string.rep(")", 32))))
t(string.rep("()", 33), "a")
print(select("#", string.find("a", string.rep("()", 32))))
t(string.rep("(", 100000), string.rep("(", 100000))
t(string.rep("a*", 3000) .. "b", string.rep("a", 100) .. "b")
t(string.rep("a?", 3000) .. "b", string.rep("a", 100) .. "b")
t(string.rep("a-", 3000) .. "b", string.rep("a", 100) .. "b")
