-- An unclosed '(' runs to the end of the pattern: Lua 5.1's do_match
-- succeeds at the end of the pattern with the capture still open
-- (CAP_UNFINISHED), and "unfinished capture" is raised only when the value
-- of that capture is asked for (push_onecapture): string.gsub with a
-- replacement string that does not use it works; find, match and gmatch,
-- which return the captures, and gsub with a function, a table or "%1"
-- raise it. A back reference to it is an "invalid capture index".
local function enc(...)
  local t = {}
  for i = 1, select("#", ...) do
    local v = select(i, ...)
    t[i] = type(v) == "string" and string.format("%q", v) or tostring(v)
  end
  return table.concat(t, ",")
end
local function p(label, f, ...)
  print(label, enc(pcall(f, ...)))
end
local function gm(s, pat)
  local t = {}
  for a, b in string.gmatch(s, pat) do t[#t + 1] = enc(a, b) end
  return table.concat(t, ";")
end

for _, pat in ipairs({ "(", "a(", "(a", "b(", "(()", "()(", "((", "((b)", "(b()",
  "(.-", "(x*", "b(.*", "^(", "%f[a](", "(a$", "a($", "($", "(%a+$", "((a)$",
  "(()$", "(b)(" }) do
  for _, s in ipairs({ "abc", "", "aa", "a$a" }) do
    p("gsub x " .. pat .. " " .. s, string.gsub, s, pat, "x")
    p("gsub %0 " .. pat .. " " .. s, string.gsub, s, pat, "<%0>")
    p("gsub 1 " .. pat .. " " .. s, string.gsub, s, pat, "x", 1)
    p("gsub %1 " .. pat .. " " .. s, string.gsub, s, pat, "%1")
    p("gsub %2 " .. pat .. " " .. s, string.gsub, s, pat, "%2")
    p("gsub fn " .. pat .. " " .. s, string.gsub, s, pat, function(...) return select("#", ...) end)
    p("gsub tbl " .. pat .. " " .. s, string.gsub, s, pat, {})
    p("find " .. pat .. " " .. s, string.find, s, pat)
    p("find 10 " .. pat .. " " .. s, string.find, s, pat, 10)
    p("match " .. pat .. " " .. s, string.match, s, pat)
    p("gmatch " .. pat .. " " .. s, gm, s, pat)
  end
end

-- back references to an unfinished capture, or past it
p("backref", string.match, "abc", "b(%1")
p("backref2", string.match, "aab", "(a)(%1")
p("backref3", string.gsub, "aab", "(a)(%1", "x")
p("backref4", string.gsub, "aab", "(a)(%2", "x")
p("plain find", string.find, "a(", "(", 1, true)
-- the error's position is the caller's, as luaL_error gives it (no tail
-- calls: LuaJIT's luaL_where does not see the caller of one)
print(pcall(function() local r = string.find("abc", "(") return r end))
print(pcall(function() local r = string.gsub("abc", "(", "%1") return r end))
print(pcall(function() for a in string.gmatch("abc", "b(") do end end))
