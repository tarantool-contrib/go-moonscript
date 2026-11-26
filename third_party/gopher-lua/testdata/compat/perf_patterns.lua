-- Pattern matching (go-moonscript perf patch: compiled patterns are cached,
-- and the captures of a failed attempt are reset for the next one): the
-- same patterns used many times, captures and position captures in
-- attempts that fail before one succeeds.
for round = 1, 3 do
  print(string.find("xxab", "()a(b)"), string.find("aXbaab", "(a)()b"))
  print(string.match("key = value", "^(%w+)%s*=%s*(%w+)$"), (string.match("  x", "^(%w+)")))
  print(string.match("hello world", "(o)(.-)(o)"), string.match("abcabc", "((a)(b))c"))
  print(string.find("a.b.c", ".", 1, true), string.find("a.b.c", "%.", 3), string.find("abc", "b", -1))
  print(string.gsub("hello world", "(o)", "[%1]"), string.gsub("abc", "", "-"))
  print(string.gsub("abc def", "%w+", "%0 %0", 1), string.gsub("x=1, y=2", "(%w+)=(%w+)", "%2=%1"))
  print(string.gsub("aaa", "a*", "<%0>"), string.gsub("one two", "()", "|"))
  print(string.match("THE (quick) fox", "%((%a+)%)"), string.find("f(a(b)c)d", "%b()"))
  print(string.match("abab", "(ab)%1"), string.match("xyyx", "(x)(y)%2%1"), (string.match("xyz", "(x)(y)%2")))
  print(string.find("aaab", "a-b"), string.find("aaab", "a+b"), string.find("b", "a?b"), string.find("", "a*"))
  print(string.match("2024-01-15", "(%d+)-(%d+)-(%d+)"), string.match("[[x]]", "%[%[(.*)%]%]"))
  print(string.find("abc", "[b-c]+"), string.find("abc", "[^a]"), string.match("a1b2", "%a%d%a(%d)"))
  print(string.find("abcabc", "bc", 3), string.find("abcabc", "bc", -2), string.find("abc", "abcd"), string.find("abc", "c", 4))
  print(string.gsub("abab", "ab", "X", 1), string.gsub("aaaa", "aa", "b"), string.gsub("a,b,,c", ",", ";"))
  print(string.match("key=val", "="), string.match("xyz", "yz", 2), (string.match("xyz", "yz", 3)))
  local lits = 0
  for w in string.gmatch("one, two, three", ", ") do lits = lits + #w end
  print(lits)
  local words = {}
  for w, p in string.gmatch("one two  three", "(%a+)()") do words[#words + 1] = w .. "@" .. p end
  print(table.concat(words, " "))
  local n = 0
  for k, v in string.gmatch("a=1, b=2, c=3", "(%w+)=(%w+)") do n = n + #k + v end
  print(n)
end
for i = 1, 2000 do
  local p = "x" .. i .. "(%d)"
  local a = string.match("ax" .. i .. "7b", p)
  if a ~= "7" then print("bad", i, a) end
end
print("dynamic patterns ok")
-- (gopher-lua's messages differ from Lua 5.1's)
print((pcall(string.find, "a", "(%")), (pcall(string.find, "a", "[a")), (pcall(string.match, "a", "%1")))
