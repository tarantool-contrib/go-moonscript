-- string.gsub: number replacements, "%<char>" in replacement strings and
-- the values a table or function may give, as in Lua 5.1
print(string.gsub("hello world", "o", 0))
print(string.gsub("hello world", "o", 1.5))
print(string.gsub("abc", "%w", "%0%0"))
print(string.gsub("abc", "%w", "%%"))
print(string.gsub("abc", "%w", "%x%-%."))
print(string.gsub("abc", "(%w)", "[%1]"))
print(string.gsub("abc", "%w", "[%1]"))
local s = string.gsub("ab", "b", "%")
print(#s, s:byte(1, -1))
print(string.gsub("abc", ".", {a = 1, b = 2.5}))
print(string.gsub("abc", ".", {a = "x", b = false}))
print(string.gsub("abc", ".", function(c) if c == "b" then return false end return nil end))
print(string.gsub("abc", ".", function(c) return c:byte() end))
local function try(...)
  local args = {...}
  local ok, err = pcall(function() return string.gsub(unpack(args, 1, 3)) end)
  print(ok, err)
end
try("abc", ".", {a = true})
try("abc", ".", {a = {}})
try("abc", ".", function() return true end)
try("abc", ".", function() return print end)
try("abc", "%w", "%2")
print((pcall(string.gsub, "abc", "b", nil)), (pcall(string.gsub, "abc", "b", true)),
  (pcall(string.gsub, "abc", "b")))
